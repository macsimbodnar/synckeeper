package engine

// W13 end to end: a folder deleted in the Drive web UI leaves exactly one
// restorable entry in each machine's system bin (T13.1), the mass-delete
// guard still counts the files inside it (T13.5), and a crash mid-removal
// converges on the next run (T13.7).

import (
	"context"
	"fmt"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/executor"
	"github.com/macsimbodnar/synckeeper/internal/guards"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// T13.1: the incident's shape, at the scale a user notices. A folder trashed
// in Drive leaves the local tree clean, the bin holding ONE folder with every
// file still inside it, and the next cycle idle.
func TestT13RemoteFolderTrashLandsInBinAsOneEntry(t *testing.T) {
	const files = 9 // under the guard's absolute floor: this test is about the bin
	fake, rootID := newWorld(t)
	a := newMachine(t, "a", fake, rootID)
	for i := 0; i < files; i++ {
		a.write(t, fmt.Sprintf("pack/vector/f%02d.svg", i), fmt.Sprintf("c-%d", i))
	}
	a.write(t, "keep.pdf", "unrelated")
	a.sync(t)
	b := newMachine(t, "b", fake, rootID)
	b.sync(t)

	if err := fake.Trash(context.Background(), remoteChildID(t, fake, rootID, "pack")); err != nil {
		t.Fatal(err)
	}
	res := b.sync(t)

	if b.exists("pack") {
		t.Error("the folder deleted in Drive is still on machine b")
	}
	if moved := b.bin.Moved(); len(moved) != 1 || moved[0] != "pack" {
		t.Fatalf("bin received %v, want exactly one entry: the folder itself", moved)
	}
	for i := 0; i < files; i++ {
		rel := fmt.Sprintf("vector/f%02d.svg", i)
		if got, want := b.binRead(t, "pack", rel), fmt.Sprintf("c-%d", i); got != want {
			t.Errorf("bin entry lost %s: %q, want %q", rel, got, want)
		}
	}
	// One action, standing for every file it took with it.
	deletes := 0
	for _, act := range res.Plan {
		if act.Type == reconcile.QuarantineLocal {
			deletes++
			if act.SubtreeFiles != files {
				t.Errorf("collapsed action covers %d files, want %d", act.SubtreeFiles, files)
			}
		}
	}
	if deletes != 1 {
		t.Errorf("plan holds %d local deletes, want 1 collapsed folder", deletes)
	}
	if got := b.read(t, "keep.pdf"); got != "unrelated" {
		t.Errorf("unrelated file disturbed: %q", got)
	}
	if res2 := b.sync(t); len(res2.Plan) != 0 {
		t.Errorf("second cycle not idle: %+v", res2.Plan)
	}
	if n, _ := b.db.ItemCount(); n != 1 {
		t.Errorf("baseline holds %d rows, want only keep.pdf", n)
	}
}

// T13.5 (A2's mirror image, W1.8.3): the mass-delete guard counts content,
// not containers — so it must see the 12 files inside the collapsed action,
// not one directory. Counting the collapsed plan instead of the plan the
// guard was handed would silence the guard exactly when a whole tree is
// disappearing, which is the one moment it exists for.
func TestT13CollapseKeepsTheMassDeleteGuardCounting(t *testing.T) {
	const files = 12
	fake, rootID := newWorld(t)
	a := newMachine(t, "a", fake, rootID)
	for i := 0; i < files; i++ {
		a.write(t, fmt.Sprintf("pack/vector/f%02d.svg", i), fmt.Sprintf("c-%d", i))
	}
	a.write(t, "keep.pdf", "unrelated")
	a.sync(t)
	b := newMachine(t, "b", fake, rootID)
	b.sync(t)
	if err := fake.Trash(context.Background(), remoteChildID(t, fake, rootID, "pack")); err != nil {
		t.Fatal(err)
	}

	// The daemon's own settings: the guard defers rather than aborting.
	res, err := b.eng.Sync(context.Background(), Options{DeferMassDelete: true})
	if err != nil {
		t.Fatalf("daemon cycle must not fail on a guard trip: %v", err)
	}
	if !res.GuardBlocked {
		t.Fatalf("guard did not fire on a %d-of-%d file deletion; plan=%+v", files, files+1, res.Plan)
	}
	if b.bin.Moved() != nil {
		t.Errorf("guard blocked but the bin received %v", b.bin.Moved())
	}
	if got := len(b.listTree(t)); got != files+1 {
		t.Errorf("guard blocked but %d files were removed anyway", files+1-got)
	}
	if got := plannedFileDeletes(res.Plan, reconcile.QuarantineLocal); got != files {
		t.Errorf("the blocked plan accounts for %d files, want %d — SubtreeFiles is what keeps later counts honest", got, files)
	}
	// Belt and braces: the guard runs before the collapse, and it also sees
	// through one — so a reordering can never quietly hide a whole tree.
	if err := guards.CheckMassDelete(res.Plan, files+1, 0.10, false); err == nil {
		t.Error("the collapsed plan is invisible to the guard; SubtreeFiles must keep it counted")
	}
}

// T13.7: a crash between the move to the bin and the DB commit. The content
// is already out of the sync dir with its rows still saying it is there; the
// next run must converge, not resurrect (invariant 6).
func TestT13CrashBetweenTrashAndCommitConverges(t *testing.T) {
	fake, rootID := newWorld(t)
	a := newMachine(t, "a", fake, rootID)
	a.write(t, "docs/a.txt", "content a")
	a.write(t, "docs/sub/b.txt", "content b")
	a.write(t, "keep.txt", "so the tree is never empty")
	a.sync(t)
	b := newMachine(t, "b", fake, rootID)
	b.sync(t)

	if err := fake.Trash(context.Background(), remoteChildID(t, fake, rootID, "docs")); err != nil {
		t.Fatal(err)
	}
	arm(t, executor.CPTrashBeforeCommit)
	syncExpectFailure(t, b)
	disarm()

	if b.exists("docs") {
		t.Fatal("the folder is in the bin; the crash was after the move")
	}
	res := b.sync(t)
	for _, act := range res.Plan {
		if act.Type == reconcile.Upload || act.Type == reconcile.MkdirRemote {
			t.Errorf("planned %s %s — the recovery must not push deleted content back", act.Type, act.RelPath)
		}
	}
	if res2 := b.sync(t); len(res2.Plan) != 0 {
		t.Errorf("still not converged: %+v", res2.Plan)
	}
	if n, _ := b.db.ItemCount(); n != 1 {
		t.Errorf("baseline holds %d rows after recovery, want only keep.txt", n)
	}
	if got := b.binRead(t, "docs", "sub/b.txt"); got != "content b" {
		t.Errorf("content lost by the crash: %q", got)
	}
}
