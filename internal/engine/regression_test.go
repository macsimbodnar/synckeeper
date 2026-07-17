package engine

// Regression tests for the 2026-07-17 review findings (testing.md, "Review
// regressions" rows). Each test is named after its ledger row.

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/executor"
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

// R4: a local edit landing between the scan and the download's rename must
// not be overwritten (spec §7 overwrite guard). The refused cycle reports a
// failure; the NEXT cycle sees local-changed + remote-changed and resolves
// it as a proper conflict — the edit ends up preserved, never lost.
func TestR4MidCycleEditBecomesConflictNotLoss(t *testing.T) {
	fake, rootID := newWorld(t)
	a := newMachine(t, "a", fake, rootID)
	b := newMachine(t, "b", fake, rootID)

	a.write(t, "f.txt", "v1")
	a.sync(t)
	b.sync(t)
	b.write(t, "f.txt", "v2-remote")
	b.sync(t)

	// While A's download of v2-remote is in flight (temp written, rename
	// pending), the "user" edits the target.
	target := filepath.Join(a.dir, "f.txt")
	executor.FaultHook = func(cp string) error {
		if cp == executor.CPDownloadTempWritten {
			return os.WriteFile(target, []byte("mid-cycle-edit"), 0o644)
		}
		return nil
	}
	defer func() { executor.FaultHook = nil }()

	res, err := a.eng.Sync(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	executor.FaultHook = nil
	if res.Failed != 1 {
		t.Fatalf("guard cycle: failed = %d, want 1 (refused download): %v", res.Failed, res.Errors)
	}
	if got := a.read(t, "f.txt"); got != "mid-cycle-edit" {
		t.Fatalf("f.txt = %q after refused cycle, want the mid-cycle edit preserved", got)
	}

	// The next cycle resolves it as an ordinary conflict.
	a.sync(t)
	if got := a.read(t, "f.txt"); got != "v2-remote" {
		t.Errorf("canonical f.txt = %q, want %q", got, "v2-remote")
	}
	cp := findConflictCopy(t, a.dir)
	if got := a.read(t, cp); got != "mid-cycle-edit" {
		t.Errorf("conflict copy %s = %q, want the mid-cycle edit", cp, got)
	}
}

// R6: a same-cycle remote cross-rename (a.txt and b.txt swap names, both
// keeping their ids) is the worst case for path-keyed row updates: the two
// local moves collide with each other's DB rows and fail transiently.
// Transient failures are accepted — but the tree MUST converge to the
// correct swapped state within a bounded number of cycles, with no content
// lost and no conflict copies.
//
// Before the fix this test exposed silent permanent divergence, worse than
// the review's "self-heals" analysis: after the moves half-failed (FS
// renamed, DB rolled back), an unprotected Record stamped the PLANNED md5
// onto whichever file actually sat at the path. The baseline then lied,
// the scanner trusted the lie (same size/mtime), and no later cycle ever
// looked again — a.txt showed stale content forever. Fixed by extending
// ProtectedBy to local moves (a record/upload of a moved file is refused
// when its move failed) and by Records verifying the scanned stat before
// overwriting the baseline's truth (invariant 7).
func TestR6RemoteFileSwapConverges(t *testing.T) {
	ctx := context.Background()
	fake, rootID := newWorld(t)
	a := newMachine(t, "a", fake, rootID)
	b := newMachine(t, "b", fake, rootID)

	a.write(t, "a.txt", "content-A")
	a.write(t, "b.txt", "content-B")
	a.sync(t)
	b.sync(t)

	// Swap remotely via a temp name, as a human (or another client) would:
	// a -> tmp, b -> a, tmp -> b. Ids are preserved throughout.
	items, err := fake.List(ctx, rootID)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, it := range items {
		ids[it.Name] = it.ID
	}
	for _, mv := range []struct{ id, name string }{
		{ids["a.txt"], "swap-tmp"},
		{ids["b.txt"], "a.txt"},
		{ids["a.txt"], "b.txt"},
	} {
		if _, err := fake.Move(ctx, mv.id, rootID, mv.name); err != nil {
			t.Fatal(err)
		}
	}

	converged := false
	for i := 1; i <= 6; i++ {
		res, err := a.eng.Sync(ctx, Options{})
		if err != nil {
			t.Fatalf("cycle %d: %v", i, err)
		}
		t.Logf("cycle %d: plan=%d executed=%d failed=%d errors=%v",
			i, len(res.Plan), res.Executed, res.Failed, res.Errors)
		if res.Failed == 0 && len(res.Plan) == 0 {
			converged = true
			break
		}
	}
	if !converged {
		t.Fatal("swap did not converge within 6 cycles")
	}

	// Final state: contents swapped, nothing lost, no conflict copies.
	if got := a.read(t, "a.txt"); got != "content-B" {
		t.Errorf("a.txt = %q, want %q", got, "content-B")
	}
	if got := a.read(t, "b.txt"); got != "content-A" {
		t.Errorf("b.txt = %q, want %q", got, "content-A")
	}
	if n := countConflictCopies(t, a.dir); n != 0 {
		t.Errorf("found %d conflict copies, want 0", n)
	}

	// Machine B walks the same transient failures and must converge too.
	bConverged := false
	for i := 1; i <= 6; i++ {
		res, err := b.eng.Sync(ctx, Options{})
		if err != nil {
			t.Fatalf("[b] cycle %d: %v", i, err)
		}
		if res.Failed == 0 && len(res.Plan) == 0 {
			bConverged = true
			break
		}
	}
	if !bConverged {
		t.Fatal("[b] swap did not converge within 6 cycles")
	}
	if got := b.read(t, "a.txt"); got != "content-B" {
		t.Errorf("[b] a.txt = %q, want %q", got, "content-B")
	}
	if got := b.read(t, "b.txt"); got != "content-A" {
		t.Errorf("[b] b.txt = %q, want %q", got, "content-A")
	}
	if n := countConflictCopies(t, b.dir); n != 0 {
		t.Errorf("[b] found %d conflict copies, want 0", n)
	}
}

func countConflictCopies(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.Contains(d.Name(), " (conflict ") {
			n++
		}
		return nil
	})
	return n
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
