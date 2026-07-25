package engine

// R26 — a folder moved to the Drive bin must propagate as a deletion, not come
// back as a re-upload. Reported from the real Linux rollout (2026-07-25):
// a synced folder was trashed in the Drive web UI, nothing was removed on
// either machine, and the daemon instead planned uploads of the whole tree
// ("error upload …: no such file or directory" once the user deleted it
// locally too). Drive trashes the *folder* only — its children keep
// trashed=false — which is exactly what Fake.Trash models here.

import (
	"context"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// remoteChildID returns the id of the named child of parentID on the fake.
func remoteChildID(t *testing.T, fake *driveclient.Fake, parentID, name string) string {
	t.Helper()
	files, err := fake.List(context.Background(), parentID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Name == name {
			return f.ID
		}
	}
	t.Fatalf("no remote child %q under %s", name, parentID)
	return ""
}

func TestRemoteFolderTrashPropagatesLocally(t *testing.T) {
	fake, rootID := newWorld(t)
	m := newMachine(t, "A", fake, rootID)

	// A folder tree, synced end to end.
	m.write(t, "docs/a.txt", "alpha")
	m.write(t, "docs/sub/b.txt", "beta")
	m.sync(t)
	docsID := remoteChildID(t, fake, rootID, "docs")

	// The user moves the folder to the bin in the Drive web UI: the folder is
	// trashed, its children are not touched.
	if err := fake.Trash(context.Background(), docsID); err != nil {
		t.Fatal(err)
	}

	res := m.sync(t)

	// The deletion must propagate: no upload may be planned for content the
	// user just deleted remotely — that resurrects it.
	for _, a := range res.Plan {
		if a.Type == reconcile.Upload || a.Type == reconcile.MkdirRemote {
			t.Errorf("planned %s %s — a remote trash must not re-upload the tree", a.Type, a.RelPath)
		}
	}
	if m.exists("docs/a.txt") || m.exists("docs/sub/b.txt") {
		t.Errorf("local files survive a remote folder trash: %v", m.listTree(t))
	}

	// And it must stay deleted: a second cycle plans nothing.
	res2 := m.sync(t)
	if len(res2.Plan) != 0 {
		t.Errorf("second cycle still plans %d actions: %v", len(res2.Plan), res2.Plan)
	}
}
