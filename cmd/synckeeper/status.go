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
	"github.com/macsimbodnar/synckeeper/internal/watch"
)

// stalenessWindow: how long since the last heartbeat before a daemon marked
// "running" is judged dead. A few heartbeat intervals of slack. The policy
// lives here (it is a multiple of the daemon's own interval); the logic that
// applies it is status.DaemonState.
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
			snap := gatherStatus(env)
			if asJSON {
				return status.WriteJSON(os.Stdout, snap)
			}
			status.PrintHuman(os.Stdout, snap)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the status as JSON")
	cmd.Flags().BoolVar(&watchMode, "watch", false, "refresh the status until interrupted")
	return cmd
}

// gatherStatus reads one snapshot through the shared read model. Everything
// machine-dependent (clock, control socket, service manager, system bin) is
// injected there, so this is the only place the real implementations are named.
func gatherStatus(env *readEnv) status.Snapshot {
	return status.Gather(status.Input{
		DB:              env.db,
		ConfigDir:       env.configDir,
		SyncDir:         env.syncDir,
		DriveFolder:     env.cfg.Drive.FolderName,
		MachineName:     env.cfg.Engine.MachineName,
		StalenessWindow: stalenessWindow,
		DaemonAlive:     daemonAlive,
	})
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
		status.PrintHuman(os.Stdout, gatherStatus(env))
		select {
		case <-ctx.Done():
			fmt.Println()
			return nil
		case <-t.C:
		}
	}
}
