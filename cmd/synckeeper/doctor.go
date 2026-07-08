package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/macsimbodnar/synckeeper/internal/doctor"
)

func newDoctorCmd() *cobra.Command {
	var repair bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Cross-check state DB against disk and Drive; --repair rebuilds a lost baseline",
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
			client, err := env.driveClient(ctx)
			if err != nil {
				return err
			}

			doc := &doctor.Doctor{DB: env.db, Client: client, Cfg: env.cfg, SyncDir: env.syncDir}
			var rep *doctor.Report
			if repair {
				rep, err = doc.Repair(ctx)
			} else {
				rep, err = doc.Check(ctx)
			}
			if err != nil {
				return err
			}
			printReport(rep, repair)
			return nil
		},
	}
	cmd.Flags().BoolVar(&repair, "repair", false, "rebuild metadata and adopt md5-matching pairs into the baseline (never deletes anything)")
	return cmd
}

func printReport(r *doctor.Report, repaired bool) {
	fmt.Printf("tracked items: %d\n", r.TrackedItems)
	if repaired {
		fmt.Printf("adopted rows:  %d\n", r.Adopted)
	}
	section := func(title string, paths []string) {
		if len(paths) == 0 {
			return
		}
		fmt.Printf("%s (%d):\n", title, len(paths))
		for i, p := range paths {
			if i == 20 {
				fmt.Printf("  ... and %d more\n", len(paths)-20)
				break
			}
			fmt.Printf("  %s\n", p)
		}
	}
	section("rows whose local file is missing", r.MissingLocal)
	section("local files modified since last sync", r.LocalModified)
	section("local files not yet tracked", r.UntrackedLocal)
	section("remote files not yet tracked", r.RemoteOnly)
	section("rows whose remote file is missing", r.RemoteMissing)
	section("orphan temp files", r.OrphanTemps)
	if r.StaleOps > 0 {
		fmt.Printf("stale pending ops: %d (will be replanned on next sync)\n", r.StaleOps)
	}
	for _, n := range r.Notes {
		fmt.Printf("note: %s\n", n)
	}
	if r.Healthy() {
		fmt.Println("Everything checks out: DB, disk, and Drive agree.")
	} else if !repaired {
		fmt.Println("Divergence above is what the next `synckeeper sync` will reconcile; run `doctor --repair` only if the state DB itself is damaged or lost.")
	} else {
		fmt.Println("Repair done. Remaining divergence above will be reconciled by the next `synckeeper sync` (uploads/downloads only, no deletions from adoption).")
	}
}
