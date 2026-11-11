//go:build unix

package scanner

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// S013: a symlink and a non-regular file are reported with the Unscanned
// marker the engine keys its hold on — without it a tracked item replaced by
// one reads as deleted locally and is trashed on Drive (audit 2026-11-11 F07).
func TestScanMarksSymlinksAndNonRegularFilesUnscanned(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "real.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0o644); err != nil {
		t.Skipf("cannot create a fifo here: %v", err)
	}

	snap, skips, err := Scan(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap["real.txt"]; !ok || len(snap) != 1 {
		t.Fatalf("snapshot = %v, want only real.txt", snap)
	}
	marked := map[string]bool{}
	for _, s := range skips {
		if s.Unscanned {
			marked[s.RelPath] = true
		}
		if s.Unreadable {
			t.Errorf("%s reported as unreadable; that marker means a directory whose contents are unknown", s.RelPath)
		}
	}
	for _, want := range []string{"linkdir", "pipe"} {
		if !marked[want] {
			t.Errorf("%s not marked Unscanned: %+v", want, skips)
		}
	}
}
