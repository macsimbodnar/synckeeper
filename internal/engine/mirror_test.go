package engine

// W16: the remote mirror (remote_nodes) must follow our own writes to Drive,
// not only Drive's change feed. Between an upload's commit and the feed
// reporting it, the baseline said "tracked, id X" while the mirror said "no
// X" — and reconcile reads that as "deleted on Drive" (plan.go, pass 1) and
// moves the just-uploaded file to the user's bin. Found in the field on
// 2026-07-30 with a 190 MB video (decisions.md).
//
// These tests are only expressible because driveclient.Fake can now withhold
// changes (E1): the fake used to make an upload instantly visible, so the
// window did not exist in tests at all.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/executor"
)

// The delete-class check is assertNoDeletes, shared with the adopt tests.

// assertMirrorCoversBaseline is the E4 invariant: at rest, every baseline row
// names a Drive id the mirror knows. The two are written together, so they
// cannot drift — a baseline row whose id is missing from the mirror is
// exactly the state reconcile misreads as a remote deletion.
func assertMirrorCoversBaseline(t *testing.T, machines ...*machine) {
	t.Helper()
	for _, m := range machines {
		items, err := m.db.AllItems()
		if err != nil {
			t.Fatalf("[%s] items: %v", m.name, err)
		}
		for _, it := range items {
			has, err := m.db.HasRemoteNode(it.DriveFileID)
			if err != nil {
				t.Fatalf("[%s] remote node %s: %v", m.name, it.DriveFileID, err)
			}
			if !has {
				t.Fatalf("[%s] baseline row %s (id %s) has no remote_nodes row: reconcile will read it as deleted on Drive",
					m.name, it.RelPath, it.DriveFileID)
			}
		}
	}
}

// driveIDOf returns the baseline row's Drive id for a rel_path.
func driveIDOf(t *testing.T, m *machine, rel string) string {
	t.Helper()
	items, err := m.db.AllItems()
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.RelPath == rel {
			return it.DriveFileID
		}
	}
	t.Fatalf("[%s] no baseline row for %s", m.name, rel)
	return ""
}

// E1: the field bug. A cycle that runs before the change feed has reported
// our own upload must plan nothing for that file — not trash it.
func TestW16UploadThenImmediateCycleKeepsFile(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)

	a.write(t, "video.mov", "190 megabytes, pretend")
	// The feed goes quiet before the upload, as Drive's does for a while
	// after one: the upload lands on Drive and the cycle after it still sees
	// the pre-upload feed.
	fake.HoldChanges(true)
	a.syncRaw(t)

	// The direct assertion: the commit put the uploaded id in the mirror.
	id := driveIDOf(t, a, "video.mov")
	has, err := a.db.HasRemoteNode(id)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Errorf("after the upload commit, id %s is not in remote_nodes", id)
	}

	// The field scenario: a watcher wake fires a second cycle 0.67 s later,
	// still inside the window.
	res := a.syncRaw(t)
	assertNoDeletes(t, res)
	if !a.exists("video.mov") {
		t.Fatal("the just-uploaded file was removed from the sync dir")
	}
	if got := a.read(t, "video.mov"); got != "190 megabytes, pretend" {
		t.Errorf("content = %q", got)
	}
	if len(a.bin.Moved()) != 0 {
		t.Errorf("the just-uploaded file went to the system bin: %v", a.bin.Moved())
	}
	assertMirrorCoversBaseline(t, a)

	// Once Drive catches up, the feed simply restates what we already knew.
	fake.HoldChanges(false)
	res = a.syncRaw(t)
	assertNoDeletes(t, res)
	if len(res.Plan) != 0 {
		t.Errorf("the caught-up feed replanned %d actions: %v", len(res.Plan), res.Plan)
	}
	assertMirrorCoversBaseline(t, a)
}

// E1: the same window around an edit of an already-tracked file, which
// commits through the same commitUpload path (UpdateRemote).
func TestW16UpdateRemoteThenImmediateCycleKeepsFile(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)

	a.write(t, "notes.txt", "v1")
	a.syncRaw(t)

	fake.HoldChanges(true)
	a.write(t, "notes.txt", "v2 edited")
	a.syncRaw(t)

	res := a.syncRaw(t)
	assertNoDeletes(t, res)
	if got := a.read(t, "notes.txt"); got != "v2 edited" {
		t.Errorf("content = %q", got)
	}
	assertMirrorCoversBaseline(t, a)
}

// E3: a file created inside a new local folder. The upload's mirror row is
// only reachable from the root if the folder MkdirRemote created is in the
// mirror too — prune drops unreachable rows, so the fix has to cover the
// whole path we created, not just the leaf.
func TestW16NewFolderThenImmediateCycleKeepsSubtree(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)

	a.write(t, "docs/deep/paper.txt", "content")
	fake.HoldChanges(true)
	a.syncRaw(t)

	res := a.syncRaw(t)
	assertNoDeletes(t, res)
	if !a.exists("docs/deep/paper.txt") {
		t.Fatal("the just-uploaded file was removed from the sync dir")
	}
	if len(a.bin.Moved()) != 0 {
		t.Errorf("content went to the system bin: %v", a.bin.Moved())
	}
	assertMirrorCoversBaseline(t, a)
}

// E3: a local rename, committed as MoveRemote. A stale mirror still shows
// the id at its old remote path, so a follow-up cycle inside the window can
// pair it there and plan the move back.
func TestW16LocalRenameThenImmediateCycleKeepsNewName(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)

	a.write(t, "before.txt", "same bytes")
	a.syncRaw(t)

	fake.HoldChanges(true)
	a.rename(t, "before.txt", "after.txt")
	a.syncRaw(t)

	res := a.syncRaw(t)
	assertNoDeletes(t, res)
	if a.exists("before.txt") {
		t.Error("the file came back at its old name")
	}
	if !a.exists("after.txt") {
		t.Fatal("the renamed file is gone")
	}
	if len(res.Plan) != 0 {
		t.Errorf("a cycle inside the window replanned %d actions: %v", len(res.Plan), res.Plan)
	}
	assertMirrorCoversBaseline(t, a)
}

// E3: a local delete, committed as TrashRemote. The mirror must lose the
// node with the baseline row, or the next cycle inside the window sees a
// remote item with no local file and downloads the deleted content back.
func TestW16LocalDeleteThenImmediateCycleStaysDeleted(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)

	a.write(t, "keep.txt", "stays") // so the delete never empties the sync dir
	a.write(t, "gone.txt", "delete me")
	a.syncRaw(t)

	fake.HoldChanges(true)
	a.remove(t, "gone.txt")
	a.syncRaw(t)

	res := a.syncRaw(t)
	if a.exists("gone.txt") {
		t.Fatal("the deleted file was downloaded back")
	}
	if len(res.Plan) != 0 {
		t.Errorf("a cycle inside the window replanned %d actions: %v", len(res.Plan), res.Plan)
	}
	assertMirrorCoversBaseline(t, a)
}

// W17 (open, Max's call — plan.md W17, decisions.md 2026-07-30 "A crashed
// upload plus a lagging feed mints a duplicate on Drive"): a crash between
// Drive storing an upload and the commit rolls both rows back by design
// (invariant 6), so the replan has no record of it — and while the change
// feed has not reported it either, the replan uploads a SECOND copy under
// the same name. Drive allows same-name siblings, so both survive: §5's
// "first by id wins" then keeps the older one and shadows the newer, which
// can revert every machine to older content.
//
// Out of W16's scope: W16 is about writes that DID commit. Closing this one
// changes the §4.6 crash-resume contract (the discarded pending-op journal
// would have to be consulted, or every upload would have to check the parent
// listing first), which is a design decision, not an implementation detail.
//
// Kept in-tree and skipped so the reproduction is not re-derived: unskip it
// with the fix and it is the red test.
func TestW17CrashedUploadWithheldFeedMintsDuplicate(t *testing.T) {
	t.Skip("open defect: see plan.md W17 — unskip with the fix")

	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	a.write(t, "anchor.txt", "anchor")
	a.syncRaw(t)

	a.write(t, "dup.txt", "v1")
	fake.HoldChanges(true) // Drive's feed has not caught up yet

	var fired atomic.Bool
	executor.FaultHook = func(name string) error {
		if name == executor.CPUploadBeforeCommit && fired.CompareAndSwap(false, true) {
			return errors.New("injected crash at " + name)
		}
		return nil
	}
	if _, err := a.eng.Sync(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	executor.FaultHook = nil

	// The replan, still inside the window: it has no record of the upload.
	if _, err := a.eng.Sync(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}

	kids, err := fake.List(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, k := range kids {
		if k.Name == "dup.txt" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("Drive holds %d nodes named dup.txt, want 1: the replan re-uploaded a file Drive already had", n)
	}
}

// E4 as a property of an ordinary two-machine round trip, with the feed
// flowing normally: the invariant is about every write path, not only the
// withheld-feed window.
func TestW16MirrorCoversBaselineAfterRoundTrip(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)

	a.write(t, "one.txt", "1")
	a.write(t, "dir/two.txt", "2")
	a.syncRaw(t)
	b.syncRaw(t)
	b.write(t, "dir/three.txt", "3")
	b.syncRaw(t)
	a.syncRaw(t)

	assertConverged(t, a, b)
	assertMirrorCoversBaseline(t, a, b)

	// And a deletion retires both rows together.
	a.remove(t, "one.txt")
	a.syncRaw(t)
	b.syncRaw(t)
	assertMirrorCoversBaseline(t, a, b)
	if _, err := fake.Get(context.Background(), driveIDOf(t, b, "dir/two.txt")); err != nil {
		t.Fatal(err)
	}
}
