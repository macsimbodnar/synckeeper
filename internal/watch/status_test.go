package watch

import (
	"testing"
	"time"
)

// A running watcher records its liveness and derives activity from the cycle
// that syncs a pre-existing file — the monitoring path read by `status`.
func TestWatcherRecordsStatusAndActivity(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)

	// Written before the watcher starts: the startup sync uploads it, and the
	// cycle's plan yields one "upload" activity entry.
	a.write(t, "notes/todo.md", "buy milk")
	startWatcher(t, a, time.Hour) // fsnotify + startup sync; poll effectively off

	waitFor(t, "daemon status recorded", 5*time.Second, func() bool {
		ds, err := a.db.GetDaemonStatus()
		return err == nil && ds.Running && ds.LastSyncAt > 0
	})

	ds, err := a.db.GetDaemonStatus()
	if err != nil {
		t.Fatal(err)
	}
	if ds.Mode != ModeWatching {
		t.Errorf("mode = %q, want %q", ds.Mode, ModeWatching)
	}
	if ds.PID == 0 || ds.StartedAt == 0 || ds.LastHeartbeatAt == 0 {
		t.Errorf("incomplete status: %+v", ds)
	}
	if ds.LastCycleJSON == "" {
		t.Error("no last-cycle summary recorded")
	}

	waitFor(t, "upload activity recorded", 5*time.Second, func() bool {
		acts, err := a.db.RecentActivity(20)
		if err != nil {
			return false
		}
		for _, act := range acts {
			if act.Kind == "upload" && act.RelPath == "notes/todo.md" {
				return true
			}
		}
		return false
	})
}

// A clean shutdown flips the recorded state to stopped so `status` doesn't
// report a dead daemon as running.
func TestWatcherMarksStoppedOnShutdown(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	cancel := startWatcher(t, a, time.Hour)

	waitFor(t, "daemon running", 5*time.Second, func() bool {
		ds, err := a.db.GetDaemonStatus()
		return err == nil && ds.Running
	})

	cancel()
	waitFor(t, "daemon marked stopped", 5*time.Second, func() bool {
		ds, err := a.db.GetDaemonStatus()
		return err == nil && !ds.Running && ds.Mode == ModeStopped
	})
}
