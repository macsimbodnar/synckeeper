package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/macsimbodnar/synckeeper/internal/auth"
	"github.com/macsimbodnar/synckeeper/internal/config"
	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/guards"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

func newInitCmd() *cobra.Command {
	var force bool
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

			if err := initialize(ctx, client, db, cfg); err != nil {
				return err
			}

			syncDir, err := cfg.SyncDir()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(syncDir, 0o755); err != nil {
				return fmt.Errorf("create sync dir: %w", err)
			}

			rootID, _ := db.GetMeta(statedb.MetaRootFolderID)
			fmt.Printf("Initialized.\n  drive folder: %q (id %s)\n  sync dir:     %s\n  config:       %s\n",
				cfg.Drive.FolderName, rootID, syncDir, cfgPath)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "re-initialize even if a state DB already exists")
	return cmd
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
// it can be tested against the fake: find or create the sync folder, fetch
// the changes start token, and record both plus a stable machine id.
func initialize(ctx context.Context, client driveclient.Client, db *statedb.DB, cfg config.Config) error {
	folder, err := driveclient.FindOrCreateFolder(ctx, client, "root", cfg.Drive.FolderName)
	if err != nil {
		return fmt.Errorf("find or create Drive folder %q: %w", cfg.Drive.FolderName, err)
	}
	startToken, err := client.StartPageToken(ctx)
	if err != nil {
		return err
	}
	if err := db.SetMeta(statedb.MetaRootFolderID, folder.ID); err != nil {
		return err
	}
	if err := db.SetMeta(statedb.MetaPageToken, startToken); err != nil {
		return err
	}
	if _, err := db.GetMeta(statedb.MetaMachineID); errors.Is(err, statedb.ErrNotFound) {
		id := make([]byte, 8)
		if _, err := rand.Read(id); err != nil {
			return err
		}
		if err := db.SetMeta(statedb.MetaMachineID, hex.EncodeToString(id)); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return nil
}
