package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/macsimbodnar/synckeeper/internal/auth"
	"github.com/macsimbodnar/synckeeper/internal/config"
	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/engine"
	"github.com/macsimbodnar/synckeeper/internal/guards"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/remotedelta"
	"github.com/macsimbodnar/synckeeper/internal/root"
	"github.com/macsimbodnar/synckeeper/internal/service"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

func newInitCmd() *cobra.Command {
	var installService, noService bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Set up (or re-sync) this machine against the Drive folder; safe to re-run",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			configDir, err := config.Dir()
			if err != nil {
				return err
			}
			lock, err := guards.AcquireInstanceLock(configDir)
			if err != nil {
				return err
			}
			defer lock.Unlock()

			cfgPath, created, err := config.WriteDefault(configDir)
			if err != nil {
				return err
			}
			if created {
				slog.Info("wrote default config", "path", cfgPath)
			}
			cfg, err := config.Load(configDir)
			if err != nil {
				return err
			}

			db, err := statedb.Open(statedb.Path(configDir))
			if err != nil {
				return err
			}
			defer db.Close()

			// `init` is idempotent (W18-B), which makes it the recovery
			// command too — so it must not drag the user through a browser
			// every time. A stored token that still works is used as-is; the
			// interactive flow runs only when there is none. `login` remains
			// the way to force a fresh one.
			ts, err := auth.TokenSource(ctx, configDir)
			if err != nil {
				if ts, err = auth.Login(ctx, configDir); err != nil {
					return err
				}
			}
			client, err := driveclient.New(ctx, ts)
			if err != nil {
				return err
			}

			rootID, err := initialize(ctx, client, db, cfg)
			if err != nil {
				return err
			}

			syncDir, err := cfg.SyncDir()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(syncDir, 0o755); err != nil {
				return fmt.Errorf("create sync dir: %w", err)
			}

			fmt.Printf("Initialized.\n  drive folder: %q (id %s)\n  sync dir:     %s\n  config:       %s\n",
				root.Name(db, cfg.Drive.FolderName), rootID, syncDir, cfgPath)

			// Always merge (W18-B). This was `--adopt`; it is now what `init`
			// does, because it is the only correct answer in every case and
			// the flag only ever asked the user to confirm the obvious. A
			// union merge over the existing baseline uploads what is local,
			// downloads what is remote, pairs identical content, and conflicts
			// the rest — it cannot delete (spec §11), so it is safe to re-run
			// and safe as the recovery path for a lost DB.
			//
			// PreferNewer is set here and nowhere else (W18-E): at the one-off
			// moment a machine joins, the more recent edit is the answer the
			// user would give to "which of these two is the real one".
			fmt.Println("Merging this machine with the Drive folder (union; nothing is deleted)…")
			eng := &engine.Engine{
				DB: db, Client: client, Cfg: cfg, SyncDir: syncDir,
				QuarantineDir: filepath.Join(configDir, "quarantine"), RootID: rootID,
			}
			res, err := eng.Sync(ctx, engine.Options{PreferNewer: true})
			if res != nil {
				printMergeResult(res)
			}
			if err != nil {
				return fmt.Errorf("merge: %w", err)
			}
			if res.Failed > 0 {
				return fmt.Errorf("%d of %d merge actions failed; re-run `synckeeper sync`", res.Failed, len(res.Plan))
			}

			// Daemon-first onboarding (spec §1): offer to install the login
			// service so syncing continues in the background. The lock is still
			// held here; the just-started daemon acquires it once init exits
			// (launchd/systemd restart it), so no init flow is needed.
			interactive := isTerminal(os.Stdin) && isTerminal(os.Stdout)
			offerServiceInstall(os.Stdin, os.Stdout, installService, noService, interactive, func() (string, error) {
				bin, err := os.Executable()
				if err != nil {
					return "", err
				}
				if bin, err = filepath.EvalSymlinks(bin); err != nil {
					return "", err
				}
				return service.Install(bin)
			})
			return nil
		},
	}
	cmd.Flags().BoolVar(&installService, "service", false, "install the login service after init without prompting")
	cmd.Flags().BoolVar(&noService, "no-service", false, "skip the login-service offer (wins over --service)")
	return cmd
}

// printMergeResult summarizes init's merge: what came down, what went up,
// what already matched, and any conflict copies kept.
func printMergeResult(res *engine.Result) {
	var down, up, adopted, conflicts int
	var conflictPaths []string
	for _, a := range res.Plan {
		switch a.Type {
		case reconcile.Download:
			down++
		case reconcile.Upload:
			up++
		case reconcile.Record:
			adopted++
		case reconcile.ConflictBackup:
			conflicts++
			conflictPaths = append(conflictPaths, a.NewRelPath)
		}
	}
	fmt.Printf("  merged: %d downloaded, %d uploaded, %d already matching, %d conflict copies\n", down, up, adopted, conflicts)
	for _, p := range conflictPaths {
		fmt.Printf("    conflict copy kept: %s\n", p)
	}
	for _, e := range res.Errors {
		fmt.Printf("    failed: %s\n", e)
	}
	for _, s := range res.Skips {
		fmt.Printf("    skipped: %s (%s)\n", s.RelPath, s.Reason)
	}
}

// initialize performs the Drive-side setup through the client interface so
// it can be tested against the fake: resolve the sync folder, fetch the
// changes start token, and record both plus a stable machine id. It returns
// the folder id.
//
// It never refuses. `--adopt` and `--force` are gone (W18-B): joining a
// non-empty Drive folder is a union merge that cannot delete (spec §11), so
// making the user assert an intention first only ever taught them to pass a
// flag without reading it. Re-running over a live install is a no-op.
func initialize(ctx context.Context, client driveclient.Client, db *statedb.DB, cfg config.Config) (string, error) {
	// Id-first (W18-A): a stored root id wins whatever the folder is called
	// now, so a rename in the Drive web UI is recorded rather than acted on.
	// Only a genuinely absent folder is created — and then the baseline is
	// reset in the same transaction, so nothing can read as deleted.
	res, err := root.Resolve(ctx, client, db, cfg.Drive.FolderName)
	if err != nil {
		return "", err
	}
	if res.Created {
		// A folder we just made is empty, and Resolve already walked the
		// mirror; there is nothing to rebuild.
		return res.ID, ensureMachineID(db)
	}
	// Rebuild the remote mirror together with a fresh page token. Resetting
	// only the token would silently skip every remote change since the last
	// consumed batch and leave a stale mirror that hides remote edits (spec
	// §12). On a fresh DB this pre-warms the mirror the first sync would
	// otherwise build.
	if err := remotedelta.ForceFullWalk(ctx, client, db, res.ID); err != nil {
		return "", fmt.Errorf("build remote mirror: %w", err)
	}
	return res.ID, ensureMachineID(db)
}

// ensureMachineID mints this machine's stable identity once. It survives every
// re-init, because conflict copies already sitting on other machines name it.
func ensureMachineID(db *statedb.DB) error {
	if _, err := db.GetMeta(statedb.MetaMachineID); errors.Is(err, statedb.ErrNotFound) {
		id := make([]byte, 8)
		if _, err := rand.Read(id); err != nil {
			return err
		}
		return db.SetMeta(statedb.MetaMachineID, hex.EncodeToString(id))
	} else if err != nil {
		return err
	}
	return nil
}
