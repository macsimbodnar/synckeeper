package engine

// W14: the mass-delete guard asks about *recoverability*, not volume. A
// deletion the user can undo from a bin they can see needs no permission,
// however large; one that lands in the private quarantine still does. And
// what is no longer blocked must still be impossible to miss.

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// trashRemoteChild moves a top-level child of the sync folder to Drive's bin,
// the way the user does it in the web UI.
func trashRemoteChild(t *testing.T, fake *driveclient.Fake, rootID, name string) {
	t.Helper()
	if err := fake.Trash(context.Background(), remoteChildID(t, fake, rootID, name)); err != nil {
		t.Fatal(err)
	}
}

// M14.1: a folder deleted in Drive that happens to be nearly everything the
// machine tracks is removed without a question, lands in the bin as one
// restorable entry, and is reported as the large deletion it was.
func TestM14RecoverableWholeTreeDeletionNeedsNoConfirmation(t *testing.T) {
	const files = 30
	fake, rootID := newWorld(t)
	a := newMachine(t, "a", fake, rootID)
	for i := 0; i < files; i++ {
		a.write(t, fmt.Sprintf("pack/f%02d.txt", i), fmt.Sprintf("c-%d", i))
	}
	a.write(t, "keep.txt", "the only survivor")
	a.sync(t)
	b := newMachine(t, "b", fake, rootID)
	b.sync(t)

	trashRemoteChild(t, fake, rootID, "pack")
	res := b.sync(t) // plain one-shot semantics: no --confirm-deletes anywhere

	if res.GuardBlocked {
		t.Fatalf("a deletion into the bin must not be held: %s", res.GuardReason)
	}
	if b.exists("pack") {
		t.Error("the folder is still on disk")
	}
	if moved := b.bin.Moved(); len(moved) != 1 || moved[0] != "pack" {
		t.Errorf("bin received %v, want one restorable folder", moved)
	}
	if !res.LargeDeletion {
		t.Error("a deletion of 30 of 31 tracked files must be reported as large")
	}
	if res.DeletedLocal != files || res.DeletedRemote != 0 {
		t.Errorf("counted %d local / %d remote deletions, want %d / 0", res.DeletedLocal, res.DeletedRemote, files)
	}
	if !res.TrashAvailable {
		t.Error("TrashAvailable must say where they went")
	}
}

// M14.2: the same deletion on a machine with no system bin is held, because
// the quarantine is not somewhere the user looks — and --confirm-deletes
// releases it, into the quarantine, losing nothing.
func TestM14UnrecoverableWholeTreeDeletionIsHeldUntilConfirmed(t *testing.T) {
	const files = 30
	fake, rootID := newWorld(t)
	a := newMachine(t, "a", fake, rootID)
	for i := 0; i < files; i++ {
		a.write(t, fmt.Sprintf("pack/f%02d.txt", i), fmt.Sprintf("c-%d", i))
	}
	a.write(t, "keep.txt", "the only survivor")
	a.sync(t)
	b := newMachine(t, "b", fake, rootID)
	b.sync(t)
	b.bin.Unavailable = true

	trashRemoteChild(t, fake, rootID, "pack")
	if _, err := b.eng.Sync(context.Background(), Options{}); err == nil {
		t.Fatal("a mass deletion into the quarantine must be refused without confirmation")
	}
	if !b.exists("pack") {
		t.Fatal("the guard blocked but the folder was removed anyway")
	}

	res, err := b.eng.Sync(context.Background(), Options{ConfirmDeletes: true})
	if err != nil || res.Failed > 0 {
		t.Fatalf("confirmed sync: err=%v failed=%d %v", err, res.Failed, res.Errors)
	}
	if b.exists("pack") {
		t.Error("the confirmed deletion did not remove the folder")
	}
	if moved := b.bin.Moved(); len(moved) != 0 {
		t.Errorf("bin received %v, but this machine has none", moved)
	}
	if res.QuarantineFell == 0 {
		t.Error("the fallback to the quarantine must be counted so the daemon can report it")
	}
	// Nothing was hard-deleted: the content is in the quarantine.
	rescued := 0
	filepath.WalkDir(b.eng.QuarantineDir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rescued++
		}
		return nil
	})
	if rescued != files {
		t.Errorf("quarantine holds %d files, want %d", rescued, files)
	}
}

// M14.3: the Drive side is never guarded — Drive's own bin holds it — and,
// with W14-M4, a locally-deleted folder is ONE trash call and one restorable
// entry there instead of one per file.
func TestM14LocalWholeTreeDeletionTrashesTheFolderInOneCall(t *testing.T) {
	const files = 30
	fake, rootID := newWorld(t)
	a := newMachine(t, "a", fake, rootID)
	for i := 0; i < files; i++ {
		a.write(t, fmt.Sprintf("pack/f%02d.txt", i), fmt.Sprintf("c-%d", i))
	}
	a.write(t, "keep.txt", "the only survivor")
	a.sync(t)
	b := newMachine(t, "b", fake, rootID)
	b.sync(t)

	before := fake.TrashCount()
	a.remove(t, "pack")
	res := a.sync(t) // no confirmation: Drive's bin is recoverable

	if res.GuardBlocked {
		t.Fatalf("a deletion into Drive's bin must not be held: %s", res.GuardReason)
	}
	if calls := fake.TrashCount() - before; calls != 1 {
		t.Errorf("made %d Trash calls, want 1 — the folder stands for its subtree", calls)
	}
	trashes := 0
	for _, act := range res.Plan {
		if act.Type == reconcile.TrashRemote {
			trashes++
			if !act.IsDir || act.SubtreeFiles != files {
				t.Errorf("action %+v, want the folder covering %d files", act, files)
			}
		}
	}
	if trashes != 1 {
		t.Errorf("plan holds %d trash_remote actions, want 1", trashes)
	}
	if res.DeletedRemote != files {
		t.Errorf("counted %d remote deletions, want %d", res.DeletedRemote, files)
	}
	if n, _ := a.db.ItemCount(); n != 1 {
		t.Errorf("baseline holds %d rows, want only keep.txt", n)
	}

	// The other machine still sees the deletion and removes its copy: one
	// trashed folder propagates the whole subtree (R26).
	b.sync(t)
	if b.exists("pack") {
		t.Error("machine b kept the folder after the collapsed remote trash")
	}
	if moved := b.bin.Moved(); len(moved) != 1 || moved[0] != "pack" {
		t.Errorf("machine b's bin received %v, want the folder as one entry", moved)
	}
	assertConverged(t, a, b)
}

// M14.5 companion: a Drive item the plan never accounted for stops the
// folder-level trash — trashing the folder would take it along.
func TestM14RemoteCollapseRefusedWhenDriveHoldsAStranger(t *testing.T) {
	fake, rootID := newWorld(t)
	a := newMachine(t, "a", fake, rootID)
	a.write(t, "pack/a.txt", "one")
	a.write(t, "pack/b.txt", "two")
	a.write(t, "keep.txt", "survivor")
	a.sync(t)

	// A stranger appears in the folder on Drive at the same moment the user
	// deletes the folder locally: the engine must not sweep it into the bin.
	packID := remoteChildID(t, fake, rootID, "pack")
	if _, err := fake.Upload(context.Background(), packID, "stranger.txt", strings.NewReader("from another machine"), 20); err != nil {
		t.Fatal(err)
	}
	a.remove(t, "pack")
	res := a.sync(t)

	for _, act := range res.Plan {
		if act.Type == reconcile.TrashRemote && act.IsDir && act.SubtreeFiles > 0 {
			t.Errorf("collapsed %+v — the folder holds an item the plan never planned to delete", act)
		}
	}
	// The stranger comes down instead of disappearing.
	if got := a.read(t, "pack/stranger.txt"); got != "from another machine" {
		t.Errorf("stranger.txt = %q, want it downloaded, not trashed", got)
	}
}
