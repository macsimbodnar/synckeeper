package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// symlinkSyncDir points m's engine at a symlink to a fresh real directory
// (~/Synckeeper -> /mnt/data/Synckeeper). m.dir stays the real directory, so
// the test's own reads and writes never depend on the engine's handling of the
// link; returns the link.
func symlinkSyncDir(t *testing.T, m *machine) string {
	t.Helper()
	realDir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "Synckeeper")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	m.dir, m.eng.SyncDir = realDir, link
	return link
}

// S007: a symlinked sync_dir behaves like the directory it names — local
// files upload, remote files download into the target, edits and deletions
// propagate both ways, and the link itself is never replaced.
func TestSymlinkedSyncDirSyncsBothWays(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	b.write(t, "from_b.txt", "b")
	b.write(t, "docs/b2.txt", "b2")
	b.sync(t)

	link := symlinkSyncDir(t, a)
	a.write(t, "from_a.txt", "a")
	a.write(t, "sub/a2.txt", "a2")
	res := a.sync(t)
	if n := auditCount(res.Plan, reconcile.TrashRemote) + auditCount(res.Plan, reconcile.QuarantineLocal); n != 0 {
		t.Fatalf("first cycle over a symlinked sync dir planned %d deletions: %+v", n, res.Plan)
	}
	b.sync(t)
	assertConverged(t, a, b)
	if got := len(a.listTree(t)); got != 4 {
		t.Fatalf("want 4 files on both machines, have %d: %v", got, keys(a.listTree(t)))
	}
	if got := auditDriveFiles(t, fake, root); got != 4 {
		t.Fatalf("want 4 files on Drive, have %d", got)
	}
	if res := a.sync(t); len(res.Plan) != 0 {
		t.Fatalf("steady state over a symlinked sync dir is not a no-op: %+v", res.Plan)
	}

	// An edit and a delete made in the linked folder reach b.
	a.write(t, "from_a.txt", "a, edited")
	a.remove(t, "from_b.txt")
	a.sync(t)
	b.sync(t)
	if b.exists("from_b.txt") || b.read(t, "from_a.txt") != "a, edited" {
		t.Fatalf("local edit/delete over a symlinked sync dir did not propagate: %v", b.listTree(t))
	}

	// A delete made on b reaches a's bin, out of the linked folder.
	b.remove(t, "docs/b2.txt")
	b.sync(t)
	a.sync(t)
	if a.exists("docs/b2.txt") {
		t.Fatal("remote delete did not reach the linked folder")
	}
	if a.binRead(t, "b2.txt", "") != "b2" {
		t.Fatalf("remote delete did not land in the bin intact: %v", a.bin.Moved())
	}
	assertConverged(t, a, b)

	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the sync_dir symlink was replaced or removed: %v %v", info, err)
	}
}

// A symlinked sync_dir whose target is gone (its volume is not mounted) is a
// hard error that names the cause — never "the folder is missing, recreate
// it", which here would fail obscurely or, worse, read as a deletion.
func TestDanglingSymlinkedSyncDirIsAHardError(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	link := symlinkSyncDir(t, a)
	a.write(t, "one.txt", "1")
	a.write(t, "docs/two.txt", "2")
	a.sync(t)

	if err := os.RemoveAll(a.dir); err != nil {
		t.Fatal(err)
	}
	res, err := a.eng.Sync(context.Background(), Options{})
	if err == nil {
		t.Fatalf("a dangling symlinked sync dir synced without error: %+v", res)
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should say the sync dir is a dangling symlink: %v", err)
	}
	if got := auditDriveFiles(t, fake, root); got != 2 {
		t.Fatalf("Drive lost files: %d of 2 left", got)
	}
	if _, err := os.Stat(a.dir); !os.IsNotExist(err) {
		t.Errorf("the link's missing target was created: %v", err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the sync_dir symlink was replaced or removed: %v %v", info, err)
	}
}
