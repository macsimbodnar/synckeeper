// Command synckeeper is the CLI entry point; see docs/spec.md.
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
)

// version is injected via -ldflags "-X main.version=...".
var version = "dev"

var verbose bool

func main() {
	root := &cobra.Command{
		Use:           "synckeeper",
		Short:         "Personal bidirectional sync between a local folder and Google Drive",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			level := slog.LevelInfo
			if verbose {
				level = slog.LevelDebug
			}
			slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
		},
	}
	root.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable debug logging")

	root.AddCommand(
		newInitCmd(),
		newLoginCmd(),
		newStatusCmd(),
		newActivityCmd(),
		newConfigCmd(),
		newAccountCmd(),
		newSyncCmd(),
		newPauseCmd(),
		newResumeCmd(),
		newReloadCmd(),
		newDoctorCmd(),
		newWatchCmd(),
		newServiceCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
