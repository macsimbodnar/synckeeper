package status

import (
	"fmt"
	"io"
	"time"
)

// PrintHuman writes the one-shot human view of a Snapshot. This is what
// `synckeeper status` prints on a terminal-free stdout (a pipe, cron, a bug
// report) and what `status --plain` forces, so its bytes are pinned by golden
// files captured from the pre-W15 renderer: the dashboard was allowed to
// change how status *looks* interactively, never what it prints to a pipe.
func PrintHuman(w io.Writer, s Snapshot) {
	ds := s.Daemon
	switch s.State {
	case StateNeverRun:
		fmt.Fprintf(w, "daemon:        not running (never started under `watch`)\n")
	case StateStopped:
		fmt.Fprintf(w, "daemon:        stopped (clean shutdown; last active %s)\n", Ago(s.Now, ds.LastHeartbeatAt))
	case StateStale:
		fmt.Fprintf(w, "daemon:        NOT RUNNING — last heartbeat %s (likely crashed)\n", Ago(s.Now, ds.LastHeartbeatAt))
	case StateRunning:
		mode := ds.Mode
		if ds.Paused {
			mode = "paused"
		}
		fmt.Fprintf(w, "daemon:        running (pid %d, up %s, mode %s)\n", ds.PID, Dur(s.Now.Sub(time.Unix(ds.StartedAt, 0))), mode)
		if ds.LastSyncAt > 0 {
			fmt.Fprintf(w, "last sync:     %s%s\n", Ago(s.Now, ds.LastSyncAt), CycleSuffix(ds.LastCycleJSON))
		}
		if ds.NextPollAt > 0 {
			fmt.Fprintf(w, "next poll:     %s\n", Until(s.Now, ds.NextPollAt))
		}
		if ds.GuardBlocked {
			fmt.Fprintf(w, "guard:         BLOCKED — %s\n", ds.GuardReason)
		}
		if ds.LastError != "" {
			// Flattened here too: rows written before the daemon learned to
			// flatten them are still in the ring, and a `last error:` that runs
			// five lines breaks the one-fact-per-line shape a script greps.
			fmt.Fprintf(w, "last error:    %s\n", OneLine(ds.LastError))
		}
	}

	fmt.Fprintf(w, "config dir:    %s\n", s.ConfigDir)
	fmt.Fprintf(w, "sync dir:      %s\n", s.SyncDir)
	fmt.Fprintf(w, "drive folder:  %q\n", s.DriveFolder)
	fmt.Fprintf(w, "machine name:  %s\n", s.MachineName)
	if s.RootID != "" {
		fmt.Fprintf(w, "root folder:   %s\n", s.RootID)
	}
	if s.TokenOK {
		fmt.Fprintf(w, "token:         present\n")
	} else {
		fmt.Fprintf(w, "token:         missing (run `synckeeper init`)\n")
	}
	fmt.Fprintf(w, "autostart:     %s\n", AutostartText(s.Autostart, s.AutostartErr))
	fmt.Fprintf(w, "tracked items: %d\n", s.Items)
	fmt.Fprintf(w, "pending ops:   %d\n", s.Pending)
	fmt.Fprintf(w, "quarantine:    %d files, %d bytes\n", s.QFiles, s.QBytes)
	fmt.Fprintf(w, "system bin:    %s\n", SystemBinLine(s.BinAvailable, s.BinDest))

	if len(s.Activity) > 0 {
		fmt.Fprintln(w, "recent activity:")
		for _, a := range s.Activity {
			line := fmt.Sprintf("  %-9s %-11s %-9s %s", Ago(s.Now, a.TS), DirectionLabel(a.Source), a.Kind, OneLine(a.RelPath))
			if a.Detail != "" {
				line += " " + OneLine(a.Detail)
			}
			fmt.Fprintln(w, line)
		}
	}
}
