package engine

// Regression tests for the 2026-07-17 review findings (testing.md, "Review
// regressions" rows). Each test is named after its ledger row.

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// R1: remote rename to a lexicographically smaller path + remote edit +
// local edit of the same file. Spec §4.5 / invariant 7: the conflict backup
// must act on the file's current local location, and the canonical download
// must never destroy the only copy of the local edit.
//
// Before the fix, the backup targeted the post-move path and sorted before
// the MoveLocal feeding it: the backup failed on a missing file, the move
// then placed the locally-edited file at the canonical path, and the
// download overwrote it — the edit was lost from both sides while the next
// cycle planned nothing.
func TestR1RemoteMoveEditVsLocalEditConflict(t *testing.T) {
	fake, rootID := newWorld(t)
	a := newMachine(t, "a", fake, rootID)
	b := newMachine(t, "b", fake, rootID)

	a.write(t, "z.txt", "v1")
	a.sync(t)
	b.sync(t)

	// Machine B renames z.txt -> a.txt (move pairing keeps the Drive id),
	// then edits the content: Drive now has the same id at a.txt with v2.
	b.rename(t, "z.txt", "a.txt")
	b.sync(t)
	b.write(t, "a.txt", "v2-remote")
	b.sync(t)

	// Machine A edits the file at its old path before seeing any of that.
	a.write(t, "z.txt", "v-local-edit")
	a.sync(t) // sync() fails the test if any action failed

	// Remote wins the canonical (post-move) name.
	if got := a.read(t, "a.txt"); got != "v2-remote" {
		t.Errorf("canonical a.txt = %q, want %q", got, "v2-remote")
	}
	if a.exists("z.txt") {
		t.Error("z.txt still present; the remote rename should have retired the old path")
	}

	// The local edit survives as a conflict copy at its old location.
	cp := findConflictCopy(t, a.dir)
	if !strings.HasPrefix(filepath.Base(cp), "z (conflict a ") {
		t.Errorf("conflict copy %q not named after the file's local location", cp)
	}
	if got := a.read(t, cp); got != "v-local-edit" {
		t.Errorf("conflict copy %s = %q, want %q", cp, got, "v-local-edit")
	}

	// Converged: a second cycle has nothing to do.
	if res := a.sync(t); len(res.Plan) != 0 {
		t.Errorf("second cycle planned %d actions, want 0: %+v", len(res.Plan), res.Plan)
	}

	// The copy was uploaded too: machine B sees both versions.
	b.sync(t)
	if got := b.read(t, "a.txt"); got != "v2-remote" {
		t.Errorf("[b] canonical a.txt = %q, want %q", got, "v2-remote")
	}
	if got := b.read(t, cp); got != "v-local-edit" {
		t.Errorf("[b] conflict copy %s = %q, want %q", cp, got, "v-local-edit")
	}
}

// R2: remote same-id dir rename (Drive web UI style) plus a new remote
// subfolder created inside it. Spec §4.5 / invariant 7: a local dir creation
// must never create the destination of a pending local move.
//
// Before the fix, MkdirLocal ran in the mkdirs stage ahead of all moves and
// its MkdirAll created the move destination; the dir MoveLocal then failed
// "file exists" — every cycle, forever (livelock until a human merged the
// folders by hand).
func TestR2RemoteDirRenameWithNewSubdir(t *testing.T) {
	ctx := context.Background()
	fake, rootID := newWorld(t)
	a := newMachine(t, "a", fake, rootID)
	b := newMachine(t, "b", fake, rootID)

	a.write(t, "docs/f.txt", "v1")
	a.sync(t)
	b.sync(t)

	// Simulate the Drive web UI: rename the folder in place (same id),
	// then create a subfolder inside it.
	items, err := fake.List(ctx, rootID)
	if err != nil {
		t.Fatal(err)
	}
	var dirID string
	for _, it := range items {
		if it.IsDir() && it.Name == "docs" {
			dirID = it.ID
		}
	}
	if dirID == "" {
		t.Fatal("docs folder not found on the fake Drive")
	}
	if _, err := fake.Move(ctx, dirID, rootID, "papers"); err != nil {
		t.Fatal(err)
	}
	if _, err := fake.Mkdir(ctx, dirID, "sub"); err != nil {
		t.Fatal(err)
	}

	// One cycle must converge cleanly (sync() fails the test on any
	// failed action — the pre-fix code failed the dir move here).
	a.sync(t)

	if got := a.read(t, "papers/f.txt"); got != "v1" {
		t.Errorf("papers/f.txt = %q, want %q", got, "v1")
	}
	if !a.exists("papers/sub") {
		t.Error("papers/sub was not created locally")
	}
	if a.exists("docs") {
		t.Error("docs still present; the rename should have moved it to papers")
	}
	if res := a.sync(t); len(res.Plan) != 0 {
		t.Errorf("second cycle planned %d actions, want 0: %+v", len(res.Plan), res.Plan)
	}

	b.sync(t)
	if got := b.read(t, "papers/f.txt"); got != "v1" {
		t.Errorf("[b] papers/f.txt = %q, want %q", got, "v1")
	}
	if !b.exists("papers/sub") {
		t.Error("[b] papers/sub missing")
	}
}

// findConflictCopy returns the rel_path of the single conflict copy under
// dir, failing the test if none (or more than one) exists.
func findConflictCopy(t *testing.T, dir string) string {
	t.Helper()
	var found []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.Contains(d.Name(), " (conflict ") {
			rel, _ := filepath.Rel(dir, p)
			found = append(found, filepath.ToSlash(rel))
		}
		return nil
	})
	if len(found) != 1 {
		t.Fatalf("want exactly one conflict copy, found %d: %v", len(found), found)
	}
	return found[0]
}
