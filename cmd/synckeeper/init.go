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
	var force, adopt, installService, noService bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Authenticate, find or create the Drive folder, and create the state DB",
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
			if err := refuseReinit(db, force); err != nil {
				return err
			}

			ts, err := auth.Login(ctx, configDir)
			if err != nil {
				return err
			}
			client, err := driveclient.New(ctx, ts)
			if err != nil {
				return err
			}

			rootID, err := initialize(ctx, client, db, cfg, adopt)
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
				cfg.Drive.FolderName, rootID, syncDir, cfgPath)

			if adopt {
				fmt.Println("Adopting: merging existing Drive contents with the local folder (nothing is deleted)…")
				eng := &engine.Engine{
					DB: db, Client: client, Cfg: cfg, SyncDir: syncDir,
					QuarantineDir: filepath.Join(configDir, "quarantine"), RootID: rootID,
				}
				res, err := eng.Sync(ctx, engine.Options{})
				if res != nil {
					printAdoptResult(res)
				}
				if err != nil {
					return fmt.Errorf("adopt merge: %w", err)
				}
				if res.Failed > 0 {
					return fmt.Errorf("%d of %d merge actions failed; re-run `synckeeper sync`", res.Failed, len(res.Plan))
				}
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
	cmd.Flags().BoolVar(&force, "force", false, "re-initialize even if a state DB already exists")
	cmd.Flags().BoolVar(&adopt, "adopt", false, "join an existing non-empty Drive folder by merging both sides (union; nothing deleted)")
	cmd.Flags().BoolVar(&installService, "service", false, "install the login service after init without prompting")
	cmd.Flags().BoolVar(&noService, "no-service", false, "skip the login-service offer (wins over --service)")
	return cmd
}

// printAdoptResult summarizes the first-merge sync: what came down, what went
// up, what already matched, and any conflict copies kept.
func printAdoptResult(res *engine.Result) {
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

// refuseReinit blocks init when the DB already tracks a Drive folder,
// unless --force is given.
func refuseReinit(db *statedb.DB, force bool) error {
	_, err := db.GetMeta(statedb.MetaRootFolderID)
	switch {
	case errors.Is(err, statedb.ErrNotFound):
		return nil
	case err != nil:
		return err
	case force:
		return nil
	default:
		return errors.New("already initialized (state DB has a root folder); use --force to re-initialize")
	}
}

// initialize performs the Drive-side setup through the client interface so
// it can be tested against the fake: resolve the sync folder, fetch the
// changes start token, and record both plus a stable machine id. It returns
// the folder id.
//
// If the folder already holds files and adopt is false it refuses, so a user
// can't silently join an existing Drive folder — joining is an explicit
// `--adopt`. Nothing is persisted on that refusal, so re-running with
// --adopt works cleanly.
func initialize(ctx context.Context, client driveclient.Client, db *statedb.DB, cfg config.Config, adopt bool) (string, error) {
	// Id-first (W18-A): a stored root id wins whatever the folder is called
	// now, so a rename in the Drive web UI is recorded rather than acted on.
	// Only a genuinely absent folder is created — and then the baseline is
	// reset in the same transaction, so nothing can read as deleted.
	res, err := root.Resolve(ctx, client, db, cfg.Drive.FolderName)
	if err != nil {
		return "", err
	}
	if res.Created {
		// A folder we just made is empty: nothing to refuse over, and
		// Resolve already walked the mirror.
		return res.ID, ensureMachineID(db)
	}
	children, err := client.List(ctx, res.ID)
	if err != nil {
		return "", err
	}
	if len(children) > 0 && !adopt {
		return "", fmt.Errorf("Drive folder %q already contains %d item(s); re-run `synckeeper init --adopt` to join it by merging both sides (union; nothing is deleted)",
			res.Name, len(children))
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
