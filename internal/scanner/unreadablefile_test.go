package scanner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// lockFile makes a file unreadable, or skips the test where that cannot be
// arranged (running as root, Windows, a filesystem that ignores the mode).
func lockFile(t *testing.T, p string) {
	t.Helper()
	if err := os.Chmod(p, 0o000); err != nil {
		t.Skipf("cannot chmod %s: %v", p, err)
	}
	t.Cleanup(func() { os.Chmod(p, 0o644) })
	if f, err := os.Open(p); err == nil {
		f.Close()
		t.Skip("this environment can read a 0000 file (running as root?)")
	}
}

// S014 (audit 2026-11-11 F08): a file the scan cannot open to hash is a
// reported skip carrying the Unscanned marker — the engine's path hold — not
// a failed scan. The rest of the tree scans normally.
func TestScanReportsUnreadableFileAndKeepsGoing(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", "hello")
	write(t, root, "sub/b.txt", "world")
	write(t, root, "locked.txt", "secret")
	lockFile(t, filepath.Join(root, "locked.txt"))

	snap, skips, err := Scan(root, nil, nil)
	if err != nil {
		t.Fatalf("one unreadable file failed the whole scan: %v", err)
	}
	for _, want := range []string{"a.txt", "sub", "sub/b.txt"} {
		if _, ok := snap[want]; !ok {
			t.Errorf("%s missing from the snapshot: %v", want, keys(snap))
		}
	}
	if _, ok := snap["locked.txt"]; ok {
		t.Error("the scan claims to have seen a file it could not read")
	}
	var found int
	for _, s := range skips {
		if s.RelPath == "locked.txt" {
			if !s.Unscanned || s.Unreadable {
				t.Errorf("locked.txt skip carries the wrong marker: %+v", s)
			}
			found++
		}
	}
	if found != 1 {
		t.Fatalf("want exactly one skip for locked.txt, got %d: %+v", found, skips)
	}
}

// A tracked file whose stat matches the baseline is never opened — its hash
// is trusted — so a lost read permission alone changes nothing.
func TestScanTrustsTheBaselineForAnUnchangedUnreadableFile(t *testing.T) {
	root := t.TempDir()
	write(t, root, "locked.txt", "secret")
	p := filepath.Join(root, "locked.txt")
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	lockFile(t, p)
	base := map[string]reconcile.BaseItem{"locked.txt": {FileID: "f1", Size: info.Size(),
		MtimeNS: info.ModTime().UnixNano(), MD5: "baseline-md5"}}
	snap, skips, err := Scan(root, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := snap["locked.txt"]; got.MD5 != "baseline-md5" || len(skips) != 0 {
		t.Fatalf("want the baseline hash trusted and no skip, got %+v, skips %+v", got, skips)
	}
}
