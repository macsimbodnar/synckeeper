//go:build darwin && cgo

package watch

import (
	"context"
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
