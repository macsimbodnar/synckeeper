package engine

// Regression for the descendant-of-a-shadowed-folder quarantine (adversarial
// review 2026-07-21). Spec §5 / decisions.md W1.9.1 promised a name collision
// "can no longer send anything to quarantine". That held only for the
// directly-colliding row: Snapshot never walks a shadowed folder's subtree,
// so its tracked descendants were neither in the snapshot nor in the shadowed
// set, and reconcile read them as remote-deleted and quarantined them.

import (
	"context"
	"os"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/names"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// TestR24DescendantOfShadowedFolderNotQuarantined reproduces the whole arc:
// machine A holds a tracked folder with a child, then a fold-equal sibling
// folder appears on Drive (another machine, or the Drive web UI) with an id
// that wins "first by id" — pushing A's real folder into the shadowed
// position. A's child must not be quarantined; its content stays put.
func TestR24DescendantOfShadowedFolderNotQuarantined(t *testing.T) {
	fake, rootID := newWorld(t)
	a := newMachine(t, "a", fake, rootID)
	if !names.CaseInsensitiveFS(a.dir) {
		t.Skip("sync dir does not fold case; this shape needs a folding FS")
	}
	ctx := context.Background()

	a.write(t, "Docs/note.txt", "keep me")
	a.sync(t) // A tracks Docs (fake-2) + Docs/note.txt (fake-3)

	// Bump the fake's id counter so the injected sibling sorts BEFORE A's
	// "fake-2" ("fake-10" < "fake-2" lexicographically) and wins first-by-id.
	for i := 0; i < 6; i++ {
		d, err := fake.Mkdir(ctx, rootID, "bump")
		if err != nil {
			t.Fatal(err)
		}
		if err := fake.Trash(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	// A fold-equal sibling of A's "Docs" folder, created elsewhere.
	if _, err := fake.Mkdir(ctx, rootID, "DOCS"); err != nil { // fake-10
		t.Fatal(err)
	}

	res, err := a.eng.Sync(ctx, Options{})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	for _, act := range res.Plan {
		if act.Type == reconcile.QuarantineLocal {
			t.Errorf("shadowed folder's descendant was quarantined: %+v (spec §5: a name collision never sends content to quarantine)", act)
		}
	}
	if got := a.read(t, "Docs/note.txt"); got != "keep me" {
		t.Errorf("Docs/note.txt = %q, want it left in place", got)
	}
	if entries, err := os.ReadDir(a.eng.QuarantineDir); err == nil && len(entries) != 0 {
		t.Errorf("quarantine is not empty: %v", entries)
	}

	// The shadowed row is surfaced as a skip, not acted on.
	var skipped bool
	for _, s := range res.Skips {
		if s.RelPath == "Docs" || s.RelPath == "Docs/note.txt" {
			skipped = true
		}
	}
	if !skipped {
		t.Errorf("expected a skip surfacing the shadowed folder or its child; skips = %v", res.Skips)
	}
}

// TestExpandShadowedCoversSubtree pins the fix at the unit boundary: a
// shadowed folder id drags its whole tracked subtree into the harmless set,
// while unrelated rows are untouched.
func TestExpandShadowedCoversSubtree(t *testing.T) {
	base := []statedb.Item{
		{DriveFileID: "F", RelPath: "d", IsDir: true},
		{DriveFileID: "G", RelPath: "d/child.txt"},
		{DriveFileID: "H", RelPath: "d/sub/deep.txt"},
		{DriveFileID: "X", RelPath: "other.txt"},
	}
	skips := []reconcile.Skip{{RelPath: "d", FileID: "F", Reason: "shadowed"}}
	got := expandShadowed(base, skips)
	for _, id := range []string{"F", "G", "H"} {
		if !got[id] {
			t.Errorf("id %q should be held harmless (a shadowed folder's subtree)", id)
		}
	}
	if got["X"] {
		t.Error("unrelated id X must not be shadowed")
	}
}
