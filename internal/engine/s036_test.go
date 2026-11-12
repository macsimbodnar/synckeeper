package engine

// S036, engine level: the overlap cause found by the wide plan fuzz shape
// (testing.md S036.*), on two machines with the real executor and the
// in-memory Drive fake.

import (
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// S036.1 — another machine renamed folder D to N and its subfolder E to F;
// this machine renamed only E to F, offline, and edited a file in it. The
// plan read E's rows at D/E, missed them, and restored them by downloads at
// N/F while uploading D/F's files as new at the same paths: refused every
// cycle (§4.5, `status`: stuck), nothing synced — once Drive's side changed a
// file in E (here: a edits g.txt). Now the inner rename is seen as done here:
// D follows to N, g comes down, the edit of f goes up over its own Drive item
// (no trash, no new item), and both machines hold one tree.
func TestS036InnerFolderRenameDoneHereOuterOnDrive(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "D/E/f.txt", "f")
	a.write(t, "D/E/g.txt", "g")
	a.write(t, "D/c.txt", "c")
	settleAll(t, a, b)

	b.rename(t, "D/E", "D/F")
	b.write(t, "D/F/f.txt", "f edited on b")

	a.rename(t, "D", "N")
	a.sync(t)
	a.rename(t, "N/E", "N/F")
	a.write(t, "N/F/g.txt", "g edited on a")
	a.sync(t)

	for _, x := range b.sync(t).Plan {
		if x.Type == reconcile.TrashRemote {
			t.Fatalf("[b] %s's Drive item trashed: the rename here is the rename on Drive", x.RelPath)
		}
	}
	settleAll(t, a, b)
	wantTrees(t, fake, root, map[string]string{
		"N/F/f.txt": "f edited on b",
		"N/F/g.txt": "g edited on a",
		"N/c.txt":   "c",
	}, "", a, b)
	if b.exists("D") {
		t.Fatalf("[b] D is still here after the rename to N")
	}
}
