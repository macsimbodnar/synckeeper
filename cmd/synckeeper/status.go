package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

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
	cmd.Flags().DurationVar(&interval, "interval", tui.DefaultInterval, "dashboard refresh interval")
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
		Refresh: func() tui.Snapshot {
			return tui.Snapshot{Status: gatherStatusN(env, dashboardActivityRows), Info: infoRows}
		},
	})
}
