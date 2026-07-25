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

// newRootCmd builds the full command tree. main() and the surface-guard test
// (surface_test.go) share it so the test walks the real CLI, not a copy.
func newRootCmd() *cobra.Command {
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
		newInfoCmd(),
		newSyncCmd(),
		newPauseCmd(),
		newResumeCmd(),
		newReloadCmd(),
		newDoctorCmd(),
		newWatchCmd(),
		newServiceCmd(),
	)
	return root
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
