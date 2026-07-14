package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/macsimbodnar/synckeeper/internal/control"
	"github.com/macsimbodnar/synckeeper/internal/engine"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

func newSyncCmd() *cobra.Command {
	var dryRun, confirmDeletes bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Run a one-shot bidirectional sync (or trigger the running daemon)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			// If the daemon is running it holds the instance lock, so a
			// standalone sync can't run — delegate to it over the socket.
			if daemonAlive() {
				if dryRun {
					return errors.New("the watch daemon is running; --dry-run needs a standalone sync. Stop the daemon first, then re-run.")
				}
				return delegateSync(confirmDeletes)
			}

			env, err := openAppEnv()
			if err != nil {
				return err
			}
			defer env.close()

			rootID, err := env.db.GetMeta(statedb.MetaRootFolderID)
			if errors.Is(err, statedb.ErrNotFound) {
				return errors.New("not initialized: run `synckeeper init` first")
			} else if err != nil {
				return err
			}
			client, err := env.driveClient(ctx)
			if err != nil {
				return err
			}

			eng := &engine.Engine{
				DB: env.db, Client: client, Cfg: env.cfg, SyncDir: env.syncDir,
				QuarantineDir: filepath.Join(env.configDir, "quarantine"), RootID: rootID,
			}
			res, err := eng.Sync(ctx, engine.Options{DryRun: dryRun, ConfirmDeletes: confirmDeletes})
			if res != nil {
				printResult(res, dryRun)
			}
			if err != nil {
				return err
			}
			if res.Failed > 0 {
				return fmt.Errorf("%d of %d actions failed; they will be retried next run", res.Failed, len(res.Plan))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan without changing anything")
	cmd.Flags().BoolVar(&confirmDeletes, "confirm-deletes", false, "allow a plan that exceeds the mass-delete threshold")
	return cmd
}

// delegateSync asks the running daemon to sync now and waits for the cycle to
// finish, reporting the outcome. Completion is detected from the daemon's
// recorded status (last-sync time or an updated cycle summary).
func delegateSync(confirmDeletes bool) error {
	env, err := openReadEnv()
	if err != nil {
		return err
	}
	defer env.close()

	var beforeSync int64
	var beforeCycle string
	if ds, err := env.db.GetDaemonStatus(); err == nil {
		beforeSync, beforeCycle = ds.LastSyncAt, ds.LastCycleJSON
	}

	argsJSON, _ := json.Marshal(map[string]bool{"confirm_deletes": confirmDeletes})
	resp, running, err := callDaemon(control.Request{Cmd: control.CmdSync, Args: argsJSON})
	if err != nil {
		return err
	}
	if !running {
		return errNoDaemon
	}
	if !resp.OK {
		return errors.New(resp.Error)
	}
	fmt.Println("Triggered a sync in the running daemon; waiting for it to finish…")

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
		ds, err := env.db.GetDaemonStatus()
		if err != nil {
			continue
		}
		if ds.GuardBlocked {
			fmt.Printf("Blocked by a guard: %s\n", ds.GuardReason)
			return nil
		}
		if ds.LastSyncAt > beforeSync || ds.LastCycleJSON != beforeCycle {
			fmt.Println("Done." + cycleSuffix(ds.LastCycleJSON))
			return nil
		}
		if ds.LastError != "" {
			fmt.Printf("Sync cycle errored: %s\n", ds.LastError)
			return nil
		}
	}
	fmt.Println("Still running; check `synckeeper status`.")
	return nil
}

func printResult(res *engine.Result, dryRun bool) {
	if len(res.Plan) == 0 {
		fmt.Println("Already in sync; nothing to do.")
	} else if dryRun {
		fmt.Printf("Plan (%d actions, dry run — nothing executed):\n", len(res.Plan))
		for _, a := range res.Plan {
			if a.NewRelPath != "" {
				fmt.Printf("  %-16s %s -> %s\n", a.Type, a.RelPath, a.NewRelPath)
			} else {
				fmt.Printf("  %-16s %s\n", a.Type, a.RelPath)
			}
		}
	} else {
		fmt.Printf("Executed %d of %d actions.\n", res.Executed, len(res.Plan))
	}
	for _, e := range res.Errors {
		fmt.Printf("  failed: %s\n", e)
	}
	for _, s := range res.Skips {
		fmt.Printf("  skipped: %s (%s)\n", s.RelPath, s.Reason)
	}
}
