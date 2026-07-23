//go:build darwin && cgo

package watch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// W3.2: the FSEvents backend wakes the sync loop on a real local change.
// Correctness never depends on the watcher (spec §8.1), so this is a wake-up
// smoke test against the real OS API, not a data-correctness gate.
func TestFSEventsBackendWakesOnChange(t *testing.T) {
	root := t.TempDir()
	woke := make(chan struct{}, 16)
	ignore := func() []string { return []string{".DS_Store"} }
	wake := func() {
		select {
		case woke <- struct{}{}:
		default:
		}
	}

	b, failed, err := newFSEventsBackend(context.Background(), root, ignore, wake)
	if err != nil {
		t.Fatalf("newFSEventsBackend: %v", err)
	}
	if failed != 0 {
		t.Errorf("failed = %d, want 0 (a whole-tree stream never fails to watch a dir)", failed)
	}
	if b.needsRebuild() {
		t.Error("FSEvents reports needsRebuild=true, want false (no per-file descriptors, nothing to leak; W3.4)")
	}
	t.Cleanup(func() { b.close() })

	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-woke:
	case <-time.After(5 * time.Second):
		t.Fatal("no wake-up after a real file change (FSEvents latency window exceeded)")
	}
}

// shouldWake is pure logic (no OS timing), so its ignore filtering is asserted
// deterministically here rather than through flaky FSEvents absence-testing.
func TestFSEventsShouldWakeFiltersIgnored(t *testing.T) {
	globs := []string{".DS_Store", "*.tmp"}
	cases := []struct {
		name    string
		changed []string
		want    bool
	}{
		{"real file", []string{"/sync/notes.txt"}, true},
		{"only ignored", []string{"/sync/.DS_Store", "/sync/sub/x.tmp"}, false},
		{"mixed", []string{"/sync/.DS_Store", "/sync/real.md"}, true},
		{"empty batch", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shouldWake(c.changed, globs); got != c.want {
				t.Errorf("shouldWake(%v) = %v, want %v", c.changed, got, c.want)
			}
		})
	}
}

// W1-scale (W3.3): FSEvents watches a >=50k-file tree with zero failed
// directories — a directory-tree stream holds no per-file descriptors, so scale
// never exhausts fds (unlike the fsnotify kqueue backend). Gated by
// SYNCKEEPER_SCALE_FILES; the acceptance gate is 50000.
func TestFSEventsScaleNoFDExhaustion(t *testing.T) {
	n := scaleFiles(t)
	root := t.TempDir()
	dirs := buildScaleTree(t, root, n)
	t.Logf("built %d files across %d dirs", n, dirs)

	woke := make(chan struct{}, 16)
	b, failed, err := newFSEventsBackend(context.Background(), root,
		func() []string { return nil },
		func() {
			select {
			case woke <- struct{}{}:
			default:
			}
		})
	if err != nil {
		t.Fatalf("newFSEventsBackend over %d files: %v", n, err)
	}
	t.Cleanup(func() { b.close() })
	if failed != 0 {
		t.Errorf("FSEvents reported %d unwatchable dirs at %d files, want 0 (no per-file fds)", failed, n)
	}

	// A change still wakes at scale.
	if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("d%06d", 0), "trigger.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-woke:
	case <-time.After(10 * time.Second):
		t.Fatal("no wake-up after a change in a 50k-file tree")
	}
}
