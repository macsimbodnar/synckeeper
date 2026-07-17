package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/macsimbodnar/synckeeper/internal/config"
)

func newConfigCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Print the effective configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, err := config.Dir()
			if err != nil {
				return err
			}
			path := filepath.Join(configDir, "config.toml")
			if _, err := os.Stat(path); os.IsNotExist(err) {
				fmt.Println("Not initialized: no config found. Run `synckeeper init`.")
				return nil
			}
			cfg, err := config.Load(configDir)
			if err != nil {
				return err
			}
			fmt.Printf("# %s\n", path)
			if err := toml.NewEncoder(os.Stdout).Encode(cfg); err != nil {
				return err
			}
			fmt.Println("\n# Edit the file above, then run `synckeeper reload` to apply hot fields")
			fmt.Println("# (poll interval, ignore globs, thresholds, retention) to the running daemon.")
			fmt.Println("# Identity/path fields (sync_dir, folder_name, machine_name) need a restart.")
			return nil
		},
	}
}
