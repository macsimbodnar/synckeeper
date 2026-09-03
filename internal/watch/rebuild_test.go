package watch

// W3.4: the periodic watcher rebuild is a per-backend decision. A backend that
// reports needsRebuild=false (FSEvents — no per-file descriptors) is left
// running; one that reports true (fsnotify on kqueue — the per-file fd leak) is
// torn down and recreated at the cadence. Which of the two fsnotify itself is
// depends on the OS — see TestRebuildIsPerPlatform. The R15 rebuild test covers the
// fsnotify rebuild path end to end; here a fake backend injected through the
// newBackend seam isolates the loop's rebuild-vs-keep decision from any real OS
// watcher.

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

type countingBackend struct {
	rebuild   bool
	closed    *atomic.Int32
	refreshes *atomic.Int32
}

func (c *countingBackend) refresh(string) int { c.refreshes.Add(1); return 0 }
func (c *countingBackend) close() error       { c.closed.Add(1); return nil }
func (c *countingBackend) needsRebuild() bool { return c.rebuild }
func (c *countingBackend) name() string       { return "fake" }

func TestRebuildIsPerBackend(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rebuild bool
	}{
		{"fsevents-style: never rebuilt", false},
		{"fsnotify-style: rebuilt at cadence", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake, root := newWorld(t)
			a := newMachine(t, "a", fake, root)
			a.write(t, "anchor.txt", "keep the dir non-empty")

			var created, closed, refreshes atomic.Int32
			newBackend = func(context.Context, string, func() []string, func()) (fsWatcher, int, error) {
				created.Add(1)
				return &countingBackend{rebuild: tc.rebuild, closed: &closed, refreshes: &refreshes}, 0, nil
			}
			t.Cleanup(func() { newBackend = newFSNotifyBackend })

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			w := &Watcher{Eng: a.eng, Poll: 15 * time.Millisecond, Debounce: 5 * time.Millisecond, rebuildCadence: 2}
			go func() {
				defer close(done)
				if err := w.Run(ctx); err != nil {
					t.Errorf("Run: %v", err)
				}
			}()
			// A waitFor Fatal below must not leak the daemon into later
			// tests (it would keep syncing against a closed DB).
			t.Cleanup(func() { cancel(); <-done })

			// Let the poll ticker drive several cadence-2 boundaries. For the
			// rebuild backend that means several recreations; for the other it
			// must mean zero.
			waitFor(t, "cycles to cross cadence boundaries", 5*time.Second, func() bool {
				return created.Load() >= 4 || refreshes.Load() >= 8
			})

			if tc.rebuild {
				if created.Load() < 2 {
					t.Errorf("rebuild backend created %d times, want ≥2 (torn down and recreated at cadence)", created.Load())
				}
			} else {
				if got := created.Load(); got != 1 {
					t.Errorf("no-rebuild backend created %d times, want exactly 1 (never rebuilt) — refreshes=%d", got, refreshes.Load())
				}
			}

			cancel()
			<-done
			// The shutdown defer closes the current backend exactly once.
			if closed.Load() < 1 {
				t.Errorf("backend never closed on shutdown")
			}
		})
	}
}

// W7-L3 (S001): the rebuild answer is per platform, not per backend name. The
// periodic teardown exists only to release kqueue's per-file descriptors, which
// inotify never allocates — so the one `fsnotifyBackend` must answer differently
// on darwin and on Linux. The table below executes on every platform; the second
// half then pins the *live* backend to the row for the OS the suite runs on, so
// the darwin answer is proved on darwin and the Linux answer on Linux.
func TestRebuildIsPerPlatform(t *testing.T) {
	table := []struct {
		goos string
		want bool
		why  string
	}{
		{"darwin", true, "kqueue: one fd per watched file, leaked on delete"},
		{"freebsd", true, "kqueue"},
		{"openbsd", true, "kqueue"},
		{"netbsd", true, "kqueue"},
		{"linux", false, "inotify: one fd per instance, one watch descriptor per directory"},
		{"windows", true, "ReadDirectoryChangesW: conservative until S006 measures it"},
	}
	for _, tc := range table {
		if got := rebuildNeededOn(tc.goos); got != tc.want {
			t.Errorf("rebuildNeededOn(%q) = %v, want %v (%s)", tc.goos, got, tc.want, tc.why)
		}
	}

	want, found := false, false
	for _, tc := range table {
		if tc.goos == runtime.GOOS {
			want, found = tc.want, true
		}
	}
	if !found {
		t.Fatalf("no expected rebuild answer recorded for GOOS=%s", runtime.GOOS)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b, _, err := newFSNotifyBackend(ctx, t.TempDir(), func() []string { return nil }, func() {})
	if err != nil {
		t.Fatalf("newFSNotifyBackend: %v", err)
	}
	defer b.close()
	if got := b.needsRebuild(); got != want {
		t.Errorf("live %s backend on %s reports needsRebuild=%v, want %v", b.name(), runtime.GOOS, got, want)
	}
}
