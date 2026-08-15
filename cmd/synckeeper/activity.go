package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/macsimbodnar/synckeeper/internal/status"
)

func newActivityCmd() *cobra.Command {
	var n int
	cmd := &cobra.Command{
		Use:   "activity",
		Short: "Show recent sync activity recorded by the watch daemon",
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

			acts, err := env.db.RecentActivity(n)
			if err != nil {
				return err
			}
			if len(acts) == 0 {
				fmt.Println("No activity recorded yet. Activity is captured while `synckeeper watch` runs.")
				return nil
			}
			for _, a := range acts { // newest first
				line := fmt.Sprintf("%s  %-11s %-9s %s",
					time.Unix(a.TS, 0).Format("2006-01-02 15:04:05"), status.DirectionLabel(a.Source), a.Kind, status.OneLine(a.RelPath))
				if a.Detail != "" {
					line += " " + status.OneLine(a.Detail)
				}
				fmt.Println(line)
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&n, "number", "n", 20, "how many recent entries to show")
	return cmd
}
