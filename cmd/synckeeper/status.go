package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/macsimbodnar/synckeeper/internal/auth"
	"github.com/macsimbodnar/synckeeper/internal/config"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show configuration, Drive folder, and state DB summary",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, err := config.Dir()
			if err != nil {
				return err
			}

			if _, err := os.Stat(filepath.Join(configDir, "config.toml")); os.IsNotExist(err) {
				fmt.Println("Not initialized: no config found. Run `synckeeper init`.")
				return nil
			}
			cfg, err := config.Load(configDir)
			if err != nil {
				return err
			}
			syncDir, err := cfg.SyncDir()
			if err != nil {
				return err
			}

			fmt.Printf("config dir:    %s\n", configDir)
			fmt.Printf("sync dir:      %s\n", syncDir)
			fmt.Printf("drive folder:  %q\n", cfg.Drive.FolderName)
			fmt.Printf("machine name:  %s\n", cfg.Engine.MachineName)

			if _, err := os.Stat(auth.TokenPath(configDir)); err == nil {
				fmt.Printf("token:         present\n")
			} else {
				fmt.Printf("token:         missing (run `synckeeper init`)\n")
			}

			dbPath := statedb.Path(configDir)
			if _, err := os.Stat(dbPath); os.IsNotExist(err) {
				fmt.Println("state db:      missing (run `synckeeper init`)")
				return nil
			}
			db, err := statedb.Open(dbPath)
			if err != nil {
				return err
			}
			defer db.Close()

			rootID, err := db.GetMeta(statedb.MetaRootFolderID)
			if errors.Is(err, statedb.ErrNotFound) {
				rootID = "(not set — run `synckeeper init`)"
			} else if err != nil {
				return err
			}
			items, err := db.ItemCount()
			if err != nil {
				return err
			}
			pending, err := db.PendingOpCount()
			if err != nil {
				return err
			}
			fmt.Printf("root folder:   %s\n", rootID)
			fmt.Printf("tracked items: %d\n", items)
			fmt.Printf("pending ops:   %d\n", pending)

			qCount, qBytes := quarantineUsage(filepath.Join(configDir, "quarantine"))
			fmt.Printf("quarantine:    %d files, %d bytes\n", qCount, qBytes)
			return nil
		},
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
