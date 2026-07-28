package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/macsimbodnar/synckeeper/internal/control"
	"github.com/macsimbodnar/synckeeper/internal/status"
	"github.com/macsimbodnar/synckeeper/internal/tui"
	"github.com/macsimbodnar/synckeeper/internal/watch"
)

// stalenessWindow: how long since the last heartbeat before a daemon marked
// "running" is judged dead. A few heartbeat intervals of slack. The policy
// lives here (it is a multiple of the daemon's own interval); the logic that
// applies it is status.DaemonState.
var stalenessWindow = 3 * watch.HeartbeatInterval

// dashboardActivityRows is how much history the dashboard pulls per refresh —
// more than the one-shot view's five, bounded so the query stays cheap.
const dashboardActivityRows = 200

func newStatusCmd() *cobra.Command {
	var asJSON, plain bool
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Live dashboard on a terminal; a one-shot report when piped or with --plain",
		Long: "Show daemon state, configuration, and recent activity.\n\n" +
			"On a terminal this is a live dashboard (1 overview, 2 activity, 3 info; ? for keys).\n" +
			"Piped, redirected, or with --plain it prints the one-shot report instead, and\n" +
			"--json emits the same data for scripts.",
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := openReadEnv()
			if errors.Is(err, errNotInitialized) {
				fmt.Println("Not initialized: no config found. Run `synckeeper init`.")
				return nil
			}
			if err != nil {
				return err
			}
			defer env.close()

			// --json is machine output and always one-shot; --plain forces the
			// one-shot human report; otherwise a terminal gets the dashboard and
			// a pipe gets the report, so `status | grep` and cron are unchanged.
			switch {
			case asJSON:
				return status.WriteJSON(os.Stdout, gatherStatus(env))
			case plain || !isTerminal(os.Stdout):
				status.PrintHuman(os.Stdout, gatherStatus(env))
				return nil
			}
			return runDashboard(cmd, env, interval)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the status as JSON (one-shot, for scripts)")
	cmd.Flags().BoolVar(&plain, "plain", false, "print the one-shot report instead of the dashboard")
	cmd.Flags().DurationVar(&interval, "interval", tui.DefaultInterval, "how often the dashboard re-reads state (it redraws ~4x/s regardless)")
	return cmd
}

// gatherStatus reads one snapshot through the shared read model. Everything
// machine-dependent (clock, control socket, service manager, system bin) is
// injected there, so this is the only place the real implementations are named.
func gatherStatus(env *readEnv) status.Snapshot {
	return gatherStatusN(env, 0)
}

func gatherStatusN(env *readEnv, activityRows int) status.Snapshot {
	return status.Gather(status.Input{
		DB:              env.db,
		ConfigDir:       env.configDir,
		SyncDir:         env.syncDir,
		DriveFolder:     env.cfg.Drive.FolderName,
		MachineName:     env.cfg.Engine.MachineName,
		StalenessWindow: stalenessWindow,
		ActivityLimit:   activityRows,
		DaemonAlive:     daemonAlive,
	})
}

// runDashboard starts the live view. The static rows are gathered once, from
// `info`'s own gatherer, so the dashboard cannot disagree with `synckeeper
// info` about a path or a default; only the live half is re-read per tick.
func runDashboard(cmd *cobra.Command, env *readEnv, interval time.Duration) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	infoRows := infoRowsForDashboard()
	if interval <= 0 {
		interval = tui.DefaultInterval
	}
	return tui.Run(ctx, tui.Options{
		Interval: interval,
		Color:    tui.ColorEnabled(),
		Actions:  dashboardActions(),
		Refresh: func() tui.Snapshot {
			live, haveLive, running := fetchLive()
			snap := status.Gather(status.Input{
				DB:              env.db,
				ConfigDir:       env.configDir,
				SyncDir:         env.syncDir,
				DriveFolder:     env.cfg.Drive.FolderName,
				MachineName:     env.cfg.Engine.MachineName,
				StalenessWindow: stalenessWindow,
				ActivityLimit:   dashboardActivityRows,
				DaemonAlive:     func() bool { return running },
			})
			if !haveLive {
				live = tui.Live{}
			}
			return tui.Snapshot{Status: snap, Info: infoRows, Live: live}
		},
	})
}

// dashboardActions are the four control-socket calls the dashboard can make —
// exactly what `sync`, `pause`, `resume` and `reload` already send, so U4 adds
// no daemon capability. Note the sync request carries no `--confirm-deletes`:
// a deletion held by the mass-delete guard stays held until the user says so on
// the command line (spec §8.1 — the daemon never self-confirms, and neither may
// a keystroke).
func dashboardActions() *tui.Actions {
	call := func(cmd string) error {
		resp, running, err := callDaemon(control.Request{Cmd: cmd})
		switch {
		case err != nil:
			return err
		case !running:
			return errNoDaemon
		case !resp.OK:
			return errors.New(resp.Error)
		}
		return nil
	}
	return &tui.Actions{
		SyncNow: func() error { return call(control.CmdSync) },
		Pause:   func() error { return call(control.CmdPause) },
		Resume:  func() error { return call(control.CmdResume) },
		Reload:  func() error { return call(control.CmdReload) },
	}
}

// fetchLive asks the daemon for the in-memory detail the state DB cannot hold
// (W15-U5). It returns the live snapshot, whether it was available, and whether
// a daemon answered at all — the last of which doubles as the aliveness check,
// so one refresh costs one socket round trip rather than two.
//
// A daemon too old to know `stat` replies OK=false ("unknown command"), which
// is still proof it is running: haveLive false, running true, and every panel
// falls back to the database. That is the whole degradation contract.
func fetchLive() (live tui.Live, haveLive, running bool) {
	resp, running, err := callDaemon(control.Request{Cmd: control.CmdStat})
	if err != nil || !running || !resp.OK {
		return tui.Live{}, false, running
	}
	var s watch.LiveSnapshot
	if err := json.Unmarshal(resp.Data, &s); err != nil {
		return tui.Live{}, false, true
	}
	live = tui.Live{
		Have:         true,
		Backend:      s.Backend,
		PollingOnly:  s.PollingOnly,
		Poll:         time.Duration(s.PollMS) * time.Millisecond,
		Debounce:     time.Duration(s.DebounceMS) * time.Millisecond,
		TickDue:      s.TickDue,
		WakePending:  s.WakePending,
		CycleRunning: s.CycleRunning,
		CycleElapsed: time.Duration(s.CycleElapsedMS) * time.Millisecond,
		CycleNumber:  s.CycleNumber,
	}
	if s.NextTickAt > 0 {
		live.NextTickAt = time.Unix(s.NextTickAt, 0)
	}
	if s.WakeDueAt > 0 {
		live.WakeDueAt = time.Unix(s.WakeDueAt, 0)
	}
	if s.CycleStartedAt > 0 {
		live.CycleStartedAt = time.Unix(s.CycleStartedAt, 0)
	}
	return live, true, true
}
