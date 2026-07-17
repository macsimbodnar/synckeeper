package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/macsimbodnar/synckeeper/internal/auth"
	"github.com/macsimbodnar/synckeeper/internal/service"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
	"github.com/macsimbodnar/synckeeper/internal/watch"
)

// stalenessWindow: how long since the last heartbeat before a daemon marked
// "running" is judged dead. A few heartbeat intervals of slack.
var stalenessWindow = 3 * watch.HeartbeatInterval

func newStatusCmd() *cobra.Command {
	var asJSON bool
	var watchMode bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show daemon state, configuration, and recent activity",
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

			if watchMode {
				return runStatusWatch(cmd, env)
			}
			view := gatherStatus(env)
			if asJSON {
				return printStatusJSON(view)
			}
			printStatusHuman(view)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the status as JSON")
	cmd.Flags().BoolVar(&watchMode, "watch", false, "refresh the status until interrupted")
	return cmd
}

// statusView is everything status shows, gathered once.
type statusView struct {
	daemon       statedb.DaemonStatus
	daemonState  string // running | stale | stopped | never-run
	rootID       string
	tokenOK      bool
	items        int
	pending      int
	qFiles       int
	qBytes       int64
	autostart    service.State
	autostartErr error
	activity     []statedb.Activity
	configDir    string
	syncDir      string
	driveFolder  string
	machineName  string
}

func gatherStatus(env *readEnv) statusView {
	v := statusView{
		configDir:   env.configDir,
		syncDir:     env.syncDir,
		driveFolder: env.cfg.Drive.FolderName,
		machineName: env.cfg.Engine.MachineName,
	}

	ds, err := env.db.GetDaemonStatus()
	found := err == nil
	v.daemon = ds
	v.daemonState = daemonState(ds, found, daemonAlive())

	if rootID, err := env.db.GetMeta(statedb.MetaRootFolderID); err == nil {
		v.rootID = rootID
	}
	if _, err := os.Stat(auth.TokenPath(env.configDir)); err == nil {
		v.tokenOK = true
	}
	v.items, _ = env.db.ItemCount()
	v.pending, _ = env.db.PendingOpCount()
	v.qFiles, v.qBytes = quarantineUsage(filepath.Join(env.configDir, "quarantine"))
	v.autostart, v.autostartErr = service.Status()
	v.activity, _ = env.db.RecentActivity(5)
	return v
}

// daemonState classifies the daemon. A live control socket (pingAlive) is
// authoritative; otherwise it falls back to recorded status and heartbeat
// freshness, which still works when the daemon is down or has no socket.
func daemonState(ds statedb.DaemonStatus, found, pingAlive bool) string {
	if pingAlive {
		return "running"
	}
	if !found || ds.StartedAt == 0 {
		return "never-run"
	}
	if !ds.Running {
		return "stopped"
	}
	if time.Since(time.Unix(ds.LastHeartbeatAt, 0)) > stalenessWindow {
		return "stale"
	}
	return "running"
}

func printStatusHuman(v statusView) {
	ds := v.daemon
	switch v.daemonState {
	case "never-run":
		fmt.Printf("daemon:        not running (never started under `watch`)\n")
	case "stopped":
		fmt.Printf("daemon:        stopped (clean shutdown; last active %s)\n", ago(ds.LastHeartbeatAt))
	case "stale":
		fmt.Printf("daemon:        NOT RUNNING — last heartbeat %s (likely crashed)\n", ago(ds.LastHeartbeatAt))
	case "running":
		mode := ds.Mode
		if ds.Paused {
			mode = "paused"
		}
		fmt.Printf("daemon:        running (pid %d, up %s, mode %s)\n", ds.PID, dur(time.Since(time.Unix(ds.StartedAt, 0))), mode)
		if ds.LastSyncAt > 0 {
			fmt.Printf("last sync:     %s%s\n", ago(ds.LastSyncAt), cycleSuffix(ds.LastCycleJSON))
		}
		if ds.NextPollAt > 0 {
			fmt.Printf("next poll:     %s\n", until(ds.NextPollAt))
		}
		if ds.GuardBlocked {
			fmt.Printf("guard:         BLOCKED — %s\n", ds.GuardReason)
		}
		if ds.LastError != "" {
			fmt.Printf("last error:    %s\n", ds.LastError)
		}
	}

	fmt.Printf("config dir:    %s\n", v.configDir)
	fmt.Printf("sync dir:      %s\n", v.syncDir)
	fmt.Printf("drive folder:  %q\n", v.driveFolder)
	fmt.Printf("machine name:  %s\n", v.machineName)
	if v.rootID != "" {
		fmt.Printf("root folder:   %s\n", v.rootID)
	}
	if v.tokenOK {
		fmt.Printf("token:         present\n")
	} else {
		fmt.Printf("token:         missing (run `synckeeper init`)\n")
	}
	fmt.Printf("autostart:     %s\n", autostartText(v.autostart, v.autostartErr))
	fmt.Printf("tracked items: %d\n", v.items)
	fmt.Printf("pending ops:   %d\n", v.pending)
	fmt.Printf("quarantine:    %d files, %d bytes\n", v.qFiles, v.qBytes)

	if len(v.activity) > 0 {
		fmt.Println("recent activity:")
		for _, a := range v.activity {
			line := fmt.Sprintf("  %-9s %-11s %-9s %s", ago(a.TS), directionLabel(a.Source), a.Kind, a.RelPath)
			if a.Detail != "" {
				line += " " + a.Detail
			}
			fmt.Println(line)
		}
	}
}

// cycleSuffix renders the last cycle summary as " — 3 actions, 3 ok, 0 failed".
func cycleSuffix(cycleJSON string) string {
	if cycleJSON == "" {
		return ""
	}
	var cs statedb.CycleSummary
	if err := json.Unmarshal([]byte(cycleJSON), &cs); err != nil {
		return ""
	}
	return fmt.Sprintf(" — %d actions, %d ok, %d failed, %dms", cs.Actions, cs.Executed, cs.Failed, cs.DurationMS)
}

func autostartText(s service.State, err error) string {
	if err != nil {
		return "unknown (" + err.Error() + ")"
	}
	if !s.Installed {
		return "not installed (run `synckeeper service install`)"
	}
	state := "installed"
	if s.Enabled {
		state += ", starts at login"
	}
	if s.Running {
		state += ", running"
	}
	return state
}

func printStatusJSON(v statusView) error {
	out := map[string]any{
		"daemon": map[string]any{
			"state":         v.daemonState,
			"pid":           v.daemon.PID,
			"mode":          v.daemon.Mode,
			"paused":        v.daemon.Paused,
			"started_at":    v.daemon.StartedAt,
			"last_sync_at":  v.daemon.LastSyncAt,
			"next_poll_at":  v.daemon.NextPollAt,
			"guard_blocked": v.daemon.GuardBlocked,
			"guard_reason":  v.daemon.GuardReason,
			"last_error":    v.daemon.LastError,
		},
		"sync_dir":      v.syncDir,
		"drive_folder":  v.driveFolder,
		"machine_name":  v.machineName,
		"root_folder":   v.rootID,
		"token_present": v.tokenOK,
		"tracked_items": v.items,
		"pending_ops":   v.pending,
		"quarantine":    map[string]any{"files": v.qFiles, "bytes": v.qBytes},
		"autostart": map[string]any{
			"installed": v.autostart.Installed,
			"enabled":   v.autostart.Enabled,
			"running":   v.autostart.Running,
		},
	}
	if v.daemon.LastCycleJSON != "" {
		var cs statedb.CycleSummary
		if json.Unmarshal([]byte(v.daemon.LastCycleJSON), &cs) == nil {
			out["daemon"].(map[string]any)["last_cycle"] = cs
		}
	}
	acts := make([]map[string]any, 0, len(v.activity))
	for _, a := range v.activity {
		acts = append(acts, map[string]any{"ts": a.TS, "kind": a.Kind, "rel_path": a.RelPath, "detail": a.Detail, "source": a.Source})
	}
	out["recent_activity"] = acts

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// runStatusWatch re-renders the human status on an interval until the user
// interrupts (Ctrl-C).
func runStatusWatch(cmd *cobra.Command, env *readEnv) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		fmt.Print("\033[H\033[2J") // clear screen, cursor home
		fmt.Printf("synckeeper — %s   (Ctrl-C to exit)\n\n", time.Now().Format("15:04:05"))
		printStatusHuman(gatherStatus(env))
		select {
		case <-ctx.Done():
			fmt.Println()
			return nil
		case <-t.C:
		}
	}
}

// quarantineUsage returns file count and total size under dir; zeros if the
// directory does not exist yet.
func quarantineUsage(dir string) (count int, bytes int64) {
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			count++
			bytes += info.Size()
		}
		return nil
	})
	return count, bytes
}
