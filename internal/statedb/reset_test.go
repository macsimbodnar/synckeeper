package statedb

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

// W18.1: ResetBaseline empties the baseline and the journal, leaves the
// mirror and the rest of meta alone, and commits atomically with the
// identity change that motivates it.
func TestResetBaselineClearsBaselineAndJournalOnly(t *testing.T) {
	db := open(t, filepath.Join(t.TempDir(), "state.db"))

	if err := db.Tx(func(tx *sql.Tx) error {
		for _, it := range []Item{
			{DriveFileID: "f1", RelPath: "a.txt", Size: 3, ContentMD5: "m1"},
			{DriveFileID: "f2", RelPath: "docs", IsDir: true},
			{DriveFileID: "f3", RelPath: "docs/b.txt", Size: 5, ContentMD5: "m2"},
		} {
			if err := UpsertItem(tx, it); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertRemoteNode(RemoteNode{FileID: "f1", ParentID: "root", Name: "a.txt", MimeType: "text/plain"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddOps([]PendingOp{{Type: "upload", RelPath: "a.txt"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetMeta(MetaMachineID, "machine-abc"); err != nil {
		t.Fatal(err)
	}

	// The identity change and the reset must land together (invariant 6): a
	// crash between them leaves the old baseline pointing at the new root,
	// which is the F1 catastrophe this whole mechanism exists to prevent.
	if err := db.Tx(func(tx *sql.Tx) error {
		if err := SetMetaTx(tx, MetaRootFolderID, "new-root"); err != nil {
			return err
		}
		return ResetBaseline(tx)
	}); err != nil {
		t.Fatal(err)
	}

	items, err := db.AllItems()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("baseline not empty after reset: %d rows", len(items))
	}
	pending, err := db.PendingOpCount()
	if err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Errorf("journal not empty after reset: %d ops", pending)
	}
	// The mirror is deliberately untouched — a recreated local sync dir has a
	// perfectly good one, and a repointed Drive root rebuilds it via
	// ForceFullWalk rather than here.
	nodes, err := db.AllRemoteNodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Errorf("remote mirror = %d rows, want 1 (reset must not touch it)", len(nodes))
	}
	if got, err := db.GetMeta(MetaRootFolderID); err != nil || got != "new-root" {
		t.Errorf("root_folder_id = %q, %v; want new-root", got, err)
	}
	if got, err := db.GetMeta(MetaMachineID); err != nil || got != "machine-abc" {
		t.Errorf("machine_id = %q, %v; want it preserved across a reset", got, err)
	}
}

// The atomicity is the point, so it is asserted rather than assumed: a failure
// after the reset inside the same transaction must roll BOTH halves back,
// leaving the old baseline with the old root id.
func TestResetBaselineRollsBackWithItsIdentityChange(t *testing.T) {
	db := open(t, filepath.Join(t.TempDir(), "state.db"))
	if err := db.SetMeta(MetaRootFolderID, "old-root"); err != nil {
		t.Fatal(err)
	}
	if err := db.Tx(func(tx *sql.Tx) error {
		return UpsertItem(tx, Item{DriveFileID: "f1", RelPath: "a.txt"})
	}); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("crash between the reset and the commit")
	if err := db.Tx(func(tx *sql.Tx) error {
		if err := SetMetaTx(tx, MetaRootFolderID, "new-root"); err != nil {
			return err
		}
		if err := ResetBaseline(tx); err != nil {
			return err
		}
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("Tx err = %v, want the injected failure", err)
	}

	items, err := db.AllItems()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Errorf("baseline = %d rows after a rolled-back reset, want the original 1", len(items))
	}
	if got, _ := db.GetMeta(MetaRootFolderID); got != "old-root" {
		t.Errorf("root_folder_id = %q after rollback, want old-root — the two halves must move together", got)
	}
}
