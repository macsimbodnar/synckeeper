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
	"fmt"
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

// W12-F2: the mass-delete guard must fire in *both* directions when a large
// tracked tree disappears — the field incident (2026-07-25) showed a daemon
// executing ~890 actions against 891 tracked files with no guard block, so
// this pins the guard at incident scale under daemon semantics
// (DeferMassDelete: strip the deletes, keep syncing, record the block).
func TestMassDeleteGuardBlocksWholeTreeDeletion(t *testing.T) {
	const files = 60 // over the absolute floor of 10, and ~98% of tracked

	t.Run("deleted locally", func(t *testing.T) {
		fake, rootID := newWorld(t)
		m := newMachine(t, "A", fake, rootID)
		for i := 0; i < files; i++ {
			m.write(t, fmt.Sprintf("pack/Vector/f%03d.svg", i), fmt.Sprintf("c-%d", i))
		}
		m.write(t, "keep.pdf", "unrelated")
		m.sync(t)

		m.remove(t, "pack")
		res, err := m.eng.Sync(context.Background(), Options{DeferMassDelete: true})
		if err != nil {
			t.Fatalf("daemon cycle must not fail on a guard trip: %v", err)
		}
		assertGuardHeld(t, res, reconcile.TrashRemote)
	})

	t.Run("trashed in drive", func(t *testing.T) {
		fake, rootID := newWorld(t)
		m := newMachine(t, "A", fake, rootID)
		for i := 0; i < files; i++ {
			m.write(t, fmt.Sprintf("pack/Vector/f%03d.svg", i), fmt.Sprintf("c-%d", i))
		}
		m.write(t, "keep.pdf", "unrelated")
		m.sync(t)
		if err := fake.Trash(context.Background(), remoteChildID(t, fake, rootID, "pack")); err != nil {
			t.Fatal(err)
		}

		res, err := m.eng.Sync(context.Background(), Options{DeferMassDelete: true})
		if err != nil {
			t.Fatalf("daemon cycle must not fail on a guard trip: %v", err)
		}
		assertGuardHeld(t, res, reconcile.QuarantineLocal)
		if got := len(m.listTree(t)); got != files+1 {
			t.Errorf("guard blocked but %d of %d local files were removed anyway", files+1-got, files)
		}
	})
}

// assertGuardHeld checks a blocked cycle: the block is recorded, the plan is
// the expected delete class, and nothing was executed.
func assertGuardHeld(t *testing.T, res *Result, want reconcile.Type) {
	t.Helper()
	if !res.GuardBlocked {
		t.Fatalf("mass-delete guard did not fire; plan=%d executed=%d", len(res.Plan), res.Executed)
	}
	if res.Executed != 0 {
		t.Errorf("guard blocked but %d actions executed", res.Executed)
	}
	deletes := 0
	for _, a := range res.Plan {
		if a.Type == want && !a.IsDir {
			deletes++
		}
	}
	if deletes == 0 {
		t.Errorf("expected %s actions in the blocked plan, got none", want)
	}
	if res.GuardReason == "" {
		t.Error("guard blocked without a reason for `status`")
	}
}
