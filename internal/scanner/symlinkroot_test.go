package scanner

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// S007 / audit 2026-11-11 F01: filepath.WalkDir does not follow a symlink
// given as its root, so a symlinked sync_dir used to scan as empty — and an
// empty scan beside a full baseline reads as "every file deleted locally".
func TestScanFollowsSymlinkedRoot(t *testing.T) {
	realDir := filepath.Join(t.TempDir(), "data")
	write(t, realDir, "a.txt", "hello")
	write(t, realDir, "sub/b.txt", "world!")
	link := filepath.Join(t.TempDir(), "Synckeeper")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}

	snap, skips, err := Scan(link, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(skips) != 0 {
		t.Errorf("unexpected skips: %+v", skips)
	}
	if len(snap) != 3 { // a.txt, sub, sub/b.txt
		t.Fatalf("a symlinked root scanned %d entries, want 3: %+v", len(snap), snap)
	}
	if snap["a.txt"].MD5 != "5d41402abc4b2a76b9719d911017c592" || !snap["sub"].IsDir {
		t.Errorf("symlinked root scanned wrong: %+v", snap)
	}
}

// A walk that reports nothing while the directory holds entries is the shape
// every "scan saw nothing" route shares (unreadable root, a root the walker
// does not descend into, …). Whatever the cause, it must fail the scan rather
// than hand reconcile an empty snapshot.
func TestScanSeeingNothingInNonEmptyRootIsAnError(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", "hello")

	orig := walkDir
	t.Cleanup(func() { walkDir = orig })
	walkDir = func(root string, fn fs.WalkDirFunc) error {
		info, err := os.Lstat(root)
		if err != nil {
			return err
		}
		return fn(root, fs.FileInfoToDirEntry(info), nil) // the root and nothing below it
	}

	snap, _, err := Scan(root, nil, nil)
	if err == nil {
		t.Fatalf("a walk that saw nothing in a non-empty dir returned a snapshot of %d entries and no error", len(snap))
	}
	if !strings.Contains(err.Error(), root) {
		t.Errorf("error should name the directory: %v", err)
	}
}

// The check must not fire on a directory that is genuinely empty, nor on one
// whose entries are all ignored or skipped: the walk visited them.
func TestScanEmptyOrAllIgnoredRootIsNotAnError(t *testing.T) {
	empty := t.TempDir()
	if snap, _, err := Scan(empty, nil, nil); err != nil || len(snap) != 0 {
		t.Fatalf("empty root: snapshot %v, err %v", snap, err)
	}

	ignored := t.TempDir()
	write(t, ignored, ".DS_Store", "x")
	if snap, _, err := Scan(ignored, nil, []string{".DS_Store"}); err != nil || len(snap) != 0 {
		t.Fatalf("all-ignored root: snapshot %v, err %v", snap, err)
	}
}
