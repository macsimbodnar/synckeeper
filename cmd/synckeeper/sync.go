package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/macsimbodnar/synckeeper/internal/engine"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

func newSyncCmd() *cobra.Command {
	var dryRun, confirmDeletes bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Run a one-shot bidirectional sync",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
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
