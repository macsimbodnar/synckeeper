package scanner

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// chmodZero makes a directory unreadable, or skips the test where that cannot
// be arranged (running as root, or a filesystem that ignores the mode).
func chmodZero(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Skipf("cannot chmod %s: %v", dir, err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	if _, err := os.ReadDir(dir); err == nil {
		t.Skip("this environment can read a 0000 directory (running as root?)")
	}
}

// W18.11 — one unreadable directory is reported and walked around, not a
// failed cycle (review F4). It used to fail `scanner.Scan` outright, so the
// whole machine stopped syncing, every cycle, visible only in the log.
func TestScanReportsUnreadableDirAndKeepsGoing(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", "hello")
	write(t, root, "locked/secret.txt", "cannot see me")
	write(t, root, "sub/b.txt", "world!")
	chmodZero(t, filepath.Join(root, "locked"))

	snap, skips, err := Scan(root, nil, nil)
	if err != nil {
		t.Fatalf("Scan returned an error for one unreadable directory: %v", err)
	}

	// The rest of the tree is scanned normally — the point of the fix.
	for _, want := range []string{"a.txt", "sub", "sub/b.txt"} {
		if _, ok := snap[want]; !ok {
			t.Errorf("%s missing from the snapshot: %v", want, keys(snap))
		}
	}
	// The folder itself is known to be there; only its contents are not.
	if item, ok := snap["locked"]; !ok || !item.IsDir {
		t.Errorf("the unreadable directory itself should still be in the snapshot as a dir: %+v", snap["locked"])
	}
	if _, ok := snap["locked/secret.txt"]; ok {
		t.Error("the scan claims to have seen a file inside a directory it could not read")
	}

	// And it is reported with the marker the engine keys on — without that
	// flag the engine cannot tell "unreadable" from "symlink" or "bad name",
	// and holding the wrong rows harmless is as wrong as holding none.
	var found int
	for _, s := range skips {
		if s.RelPath == "locked" && s.Unreadable {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("want exactly one Unreadable skip for %q, got %d: %+v", "locked", found, skips)
	}
}

func keys(m map[string]reconcile.LocalItem) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
