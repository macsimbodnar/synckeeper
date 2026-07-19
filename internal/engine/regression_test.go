package engine

// Regression tests for the 2026-07-17 review findings (testing.md, "Review
// regressions" rows). Each test is named after its ledger row.

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/executor"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
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

// R7 (adversarial analysis 2026-07-17): a remote rename whose destination name
// collides with an unrelated, untracked local file. The W1 review hardened
// downloads (R4) and records (R6) against clobbering, but MoveLocal — the third
// primitive that overwrites a local path by os.Rename — had no guard: the move
// silently destroyed the local file and reported success.
//
// The fix preserves the occupant as a conflict copy (backed up and uploaded)
// and lands the remote-canonical name via the move, with the move refused if
// the protecting backup fails (invariant 7).
func TestR7RemoteMoveOntoUntrackedLocalFilePreserved(t *testing.T) {
	ctx := context.Background()
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)

	a.write(t, "a.txt", "content-A")
	a.sync(t)

	// Rename a.txt -> b.txt remotely, same id (a move, not delete + create).
	children, _ := fake.List(ctx, root)
	var xid string
	for _, c := range children {
		if c.Name == "a.txt" {
			xid = c.ID
		}
	}
	if _, err := fake.Move(ctx, xid, root, "b.txt"); err != nil {
		t.Fatal(err)
	}

	// The user already has an unrelated precious file sitting at b.txt.
	a.write(t, "b.txt", "PRECIOUS-local-b")

	// One cycle: sync() fails the test on any failed action.
	a.sync(t)

	// The remote-canonical content landed at b.txt.
	if got := a.read(t, "b.txt"); got != "content-A" {
		t.Errorf("canonical b.txt = %q, want the moved content-A", got)
	}
	// The precious local file survives as a conflict copy — nothing lost.
	cp := findConflictCopy(t, a.dir)
	if got := a.read(t, cp); got != "PRECIOUS-local-b" {
		t.Errorf("conflict copy %s = %q, want PRECIOUS-local-b", cp, got)
	}
	// Converged.
	if res := a.sync(t); len(res.Plan) != 0 {
		t.Errorf("second cycle planned %d actions, want 0: %+v", len(res.Plan), res.Plan)
	}
	// The copy reached Drive too: a fresh machine sees both versions.
	b := newMachine(t, "b", fake, root)
	b.sync(t)
	if got := b.read(t, "b.txt"); got != "content-A" {
		t.Errorf("[b] b.txt = %q, want content-A", got)
	}
	if got := b.read(t, cp); got != "PRECIOUS-local-b" {
		t.Errorf("[b] conflict copy %s = %q, want PRECIOUS-local-b", cp, got)
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

// R13 (engine level): remote deletes a file; the user edits it locally
// after the scan but before the quarantine executes. The edit must win the
// cycle ("edit beats delete", spec §4.2) and re-upload next cycle.
func TestR13MidCycleEditWinsOverQuarantine(t *testing.T) {
	fake, rootID := newWorld(t)
	a := newMachine(t, "a", fake, rootID)
	a.write(t, "a.txt", "v1")
	a.sync(t)

	items, err := a.db.AllItems()
	if err != nil {
		t.Fatal(err)
	}
	if err := fake.Trash(context.Background(), items[0].DriveFileID); err != nil {
		t.Fatal(err)
	}

	// The mid-cycle edit, injected between the scan and the quarantine move.
	target := filepath.Join(a.dir, "a.txt")
	executor.FaultHook = func(cp string) error {
		if cp == executor.CPQuarantineBeforeMove {
			if err := os.WriteFile(target, []byte("edited mid-cycle"), 0o644); err != nil {
				t.Error(err)
			}
		}
		return nil
	}
	defer func() { executor.FaultHook = nil }()

	res, err := a.eng.Sync(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	executor.FaultHook = nil
	if got := a.read(t, "a.txt"); got != "edited mid-cycle" {
		t.Fatalf("local content = %q; the mid-cycle edit must survive in place (cycle: executed=%d failed=%d errors=%v)",
			got, res.Executed, res.Failed, res.Errors)
	}

	// Next cycle: edit beats delete — the file resurrects to Drive.
	a.sync(t)
	b := newMachine(t, "b", fake, rootID)
	b.sync(t)
	if got := b.read(t, "a.txt"); got != "edited mid-cycle" {
		t.Fatalf("machine b sees %q, want the resurrected edit", got)
	}
}

// R9 (engine): a local directory rename is one remote move — the Drive
// folder keeps its id, the plan contains no delete-class action, and the
// directory row survives as is_dir = 1. A second machine receives it as a
// local dir move with contents intact.
func TestR9LocalDirRenameKeepsFolderIdentity(t *testing.T) {
	fake, rootID := newWorld(t)
	a := newMachine(t, "a", fake, rootID)
	a.write(t, "docs/one.txt", "c1")
	a.write(t, "docs/sub/two.txt", "c2")
	a.sync(t)
	b := newMachine(t, "b", fake, rootID)
	b.sync(t)

	items, err := a.db.AllItems()
	if err != nil {
		t.Fatal(err)
	}
	var dirID string
	for _, it := range items {
		if it.RelPath == "docs" {
			dirID = it.DriveFileID
		}
	}
	if dirID == "" {
		t.Fatal("no baseline row for docs/")
	}

	a.rename(t, "docs", "papers")
	res := a.sync(t)
	for _, act := range res.Plan {
		switch act.Type {
		case reconcile.TrashRemote, reconcile.QuarantineLocal, reconcile.MkdirRemote:
			t.Errorf("folder rename planned %s %s — must be a pure move", act.Type, act.RelPath)
		}
	}
	items, _ = a.db.AllItems()
	found := false
	for _, it := range items {
		if it.RelPath == "papers" {
			found = true
			if it.DriveFileID != dirID {
				t.Errorf("folder id churned: %s -> %s", dirID, it.DriveFileID)
			}
			if !it.IsDir {
				t.Error("directory row lost is_dir across the move")
			}
		}
		if it.RelPath == "docs" || it.RelPath == "docs/one.txt" {
			t.Errorf("stale baseline row at old path %s", it.RelPath)
		}
	}
	if !found {
		t.Fatal("no baseline row for papers/ after the rename")
	}
	if res2 := a.sync(t); len(res2.Plan) != 0 {
		t.Errorf("second cycle not idle: %+v", res2.Plan)
	}

	b.sync(t)
	if got := b.read(t, "papers/sub/two.txt"); got != "c2" {
		t.Errorf("machine b papers/sub/two.txt = %q", got)
	}
	if b.exists("docs") {
		t.Error("machine b still has the old docs/ dir")
	}
}

// R9 (engine): a file added remotely while the dir is renamed locally lands
// under the NEW name; no zombie source dir, folder id stable.
func TestR9ConcurrentRemoteAddUnderRenamedDir(t *testing.T) {
	fake, rootID := newWorld(t)
	a := newMachine(t, "a", fake, rootID)
	a.write(t, "docs/one.txt", "c1")
	a.sync(t)

	items, _ := a.db.AllItems()
	var dirID string
	for _, it := range items {
		if it.RelPath == "docs" {
			dirID = it.DriveFileID
		}
	}
	if _, err := fake.Upload(context.Background(), dirID, "new.txt", strings.NewReader("fresh"), 5); err != nil {
		t.Fatal(err)
	}
	a.rename(t, "docs", "papers")
	a.sync(t)
	a.sync(t)

	if got := a.read(t, "papers/new.txt"); got != "fresh" {
		t.Errorf("papers/new.txt = %q, want the remotely added content", got)
	}
	if a.exists("docs") {
		t.Error("zombie docs/ dir resurrected locally")
	}
	items, _ = a.db.AllItems()
	for _, it := range items {
		if it.RelPath == "papers" && it.DriveFileID != dirID {
			t.Errorf("folder id churned: %s -> %s", dirID, it.DriveFileID)
		}
	}
}

// R18 (spec §7): a MoveRemote commit never restates local truth. An edit
// landing between the scan and the commit stays visibly dirty — the row
// keeps the SCANNED stat and md5 — and uploads on the next cycle.
func TestR18MoveRemoteCommitKeepsLocalTruth(t *testing.T) {
	fake, rootID := newWorld(t)
	a := newMachine(t, "a", fake, rootID)
	a.write(t, "a.txt", "v1")
	a.sync(t)

	items, _ := a.db.AllItems()
	var fileID, md5v1 string
	var sizeV1, mtimeV1 int64
	for _, it := range items {
		if it.RelPath == "a.txt" {
			fileID, md5v1, sizeV1, mtimeV1 = it.DriveFileID, it.ContentMD5, it.Size, it.LocalMtimeNS
		}
	}

	// The user renames; the scan pairs the move; then the edit lands before
	// the executor commits — same size, new content, new mtime.
	a.rename(t, "a.txt", "b.txt")
	if err := os.WriteFile(filepath.Join(a.dir, "b.txt"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Minute)
	os.Chtimes(filepath.Join(a.dir, "b.txt"), past, past)

	x := &executor.Executor{DB: a.db, Client: fake, SyncDir: a.dir,
		QuarantineDir: filepath.Join(filepath.Dir(a.dir), "quarantine"), RootID: rootID}
	sum, err := x.Apply(context.Background(), []reconcile.Action{{
		Type: reconcile.MoveRemote, RelPath: "a.txt", NewRelPath: "b.txt",
		FileID: fileID, MD5: md5v1, Size: sizeV1,
	}})
	if err != nil || sum.Failed != 0 {
		t.Fatalf("move apply: %v / %+v", err, sum)
	}

	items, _ = a.db.AllItems()
	for _, it := range items {
		if it.RelPath != "b.txt" {
			continue
		}
		if it.ContentMD5 != md5v1 || it.Size != sizeV1 || it.LocalMtimeNS != mtimeV1 {
			t.Errorf("commit restated local truth: md5=%s size=%d mtime=%d, want scanned %s/%d/%d",
				it.ContentMD5, it.Size, it.LocalMtimeNS, md5v1, sizeV1, mtimeV1)
		}
	}

	// The edit must reach Drive on the next ordinary cycle.
	a.sync(t)
	body, err := fake.Download(context.Background(), fileID)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8)
	n, _ := body.Read(buf)
	body.Close()
	if got := string(buf[:n]); got != "v2" {
		t.Errorf("drive content = %q, want the mid-cycle edit uploaded (silent divergence)", got)
	}
}

// R10 / G4 (spec §6): renaming a tree of empty folders — no pairing
// evidence, so it legitimately syncs as delete + create — must not trip the
// mass-delete guard: only file-class deletions count.
func TestR10EmptyFolderTreeRenameDoesNotTripGuard(t *testing.T) {
	fake, rootID := newWorld(t)
	a := newMachine(t, "a", fake, rootID)
	a.write(t, "keep.txt", "content")
	for i := 1; i <= 12; i++ {
		if err := os.MkdirAll(filepath.Join(a.dir, "docs", fmt.Sprintf("sub%02d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.sync(t)

	a.rename(t, "docs", "papers")
	res, err := a.eng.Sync(context.Background(), Options{})
	if err != nil {
		t.Fatalf("one-shot sync tripped the guard on a folder reorganisation: %v", err)
	}
	if res.Failed > 0 {
		t.Fatalf("cycle had failures: %v", res.Errors)
	}
	if res2 := a.sync(t); len(res2.Plan) != 0 {
		t.Errorf("did not converge in one cycle: %+v", res2.Plan)
	}
	if !a.exists("papers/sub07") || a.exists("docs") {
		t.Error("rename did not converge locally")
	}
}

// R10 / G4, the daemon half: with DeferMassDelete set (as the daemon runs),
// the same reorganisation must not leave a standing guard block.
func TestR10EmptyFolderTreeRenameDoesNotWedgeDaemon(t *testing.T) {
	fake, rootID := newWorld(t)
	a := newMachine(t, "a", fake, rootID)
	a.write(t, "keep.txt", "content")
	for i := 1; i <= 12; i++ {
		if err := os.MkdirAll(filepath.Join(a.dir, "docs", fmt.Sprintf("sub%02d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.sync(t)

	a.rename(t, "docs", "papers")
	res, err := a.eng.Sync(context.Background(), Options{DeferMassDelete: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.GuardBlocked {
		t.Fatalf("daemon guard-blocked on a folder reorganisation: %s", res.GuardReason)
	}
	res2, err := a.eng.Sync(context.Background(), Options{DeferMassDelete: true})
	if err != nil {
		t.Fatal(err)
	}
	if res2.GuardBlocked || len(res2.Plan) != 0 {
		t.Errorf("standing block or non-convergence: blocked=%v plan=%+v", res2.GuardBlocked, res2.Plan)
	}
}

// R11 (engine): machine B renames a folder remotely and adds x.txt inside
// it; machine A creates its own x.txt under the old folder name before
// syncing. The two must meet at the post-move path as an ordinary both-new
// conflict — both versions survive, exactly one x.txt on Drive.
func TestR11NewLocalFileUnderRemotelyMovedDirBecomesConflict(t *testing.T) {
	ctx := context.Background()
	fake, rootID := newWorld(t)
	a := newMachine(t, "a", fake, rootID)
	a.write(t, "docs/seed.txt", "seed")
	a.sync(t)

	items, _ := a.db.AllItems()
	var dirID string
	for _, it := range items {
		if it.RelPath == "docs" {
			dirID = it.DriveFileID
		}
	}
	if _, err := fake.Move(ctx, dirID, rootID, "papers"); err != nil {
		t.Fatal(err)
	}
	if _, err := fake.Upload(ctx, dirID, "x.txt", strings.NewReader("remote-x"), 8); err != nil {
		t.Fatal(err)
	}
	a.write(t, "docs/x.txt", "local-x")

	a.sync(t)
	if got := a.read(t, "papers/x.txt"); got != "remote-x" {
		t.Errorf("canonical papers/x.txt = %q, want remote content", got)
	}
	cp := findConflictCopy(t, a.dir)
	if got := a.read(t, cp); got != "local-x" {
		t.Errorf("conflict copy %s = %q, want the local version preserved", cp, got)
	}
	kids, err := fake.List(ctx, dirID)
	if err != nil {
		t.Fatal(err)
	}
	named := 0
	for _, k := range kids {
		if k.Name == "x.txt" {
			named++
		}
	}
	if named != 1 {
		t.Errorf("drive holds %d files named x.txt in the folder, want exactly 1 (no duplicate upload)", named)
	}
	if res := a.sync(t); len(res.Plan) != 0 {
		t.Errorf("did not converge: %+v", res.Plan)
	}
}

// R16 (A9, engine): the planted crash state of a remote-driven dir move —
// Drive renamed d to n (same folder id), our executor renamed the empty dir
// on disk and crashed before the DB commit. The next cycle must adopt, not
// trash: the folder id survives on Drive, the row lands at n, and no
// delete-class action fires (invariant 6 — a remote delete must trace to a
// user deletion, not to our crash).
func TestR16CrashedDirMoveKeepsRemoteFolder(t *testing.T) {
	fake, rootID := newWorld(t)
	a := newMachine(t, "a", fake, rootID)
	if err := os.MkdirAll(filepath.Join(a.dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	a.sync(t)
	items, err := a.db.AllItems()
	if err != nil {
		t.Fatal(err)
	}
	var dirID string
	for _, it := range items {
		if it.RelPath == "d" {
			dirID = it.DriveFileID
		}
	}
	if dirID == "" {
		t.Fatal("no baseline row for d/")
	}

	// The remote rename, then the crashed local half: disk renamed, DB not.
	if _, err := fake.Move(context.Background(), dirID, rootID, "n"); err != nil {
		t.Fatal(err)
	}
	a.rename(t, "d", "n")

	res := a.sync(t)
	for _, act := range res.Plan {
		switch act.Type {
		case reconcile.TrashRemote, reconcile.QuarantineLocal, reconcile.MkdirRemote:
			t.Errorf("crash recovery planned %s %s — must adopt, never delete or re-create", act.Type, act.RelPath)
		}
	}
	children, err := fake.List(context.Background(), rootID)
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 1 || children[0].ID != dirID || children[0].Name != "n" {
		t.Fatalf("Drive after recovery = %+v, want the original folder %s alive at n", children, dirID)
	}
	items, _ = a.db.AllItems()
	adopted := false
	for _, it := range items {
		if it.RelPath == "n" && it.DriveFileID == dirID && it.IsDir {
			adopted = true
		}
		if it.RelPath == "d" {
			t.Errorf("stale baseline row at d")
		}
	}
	if !adopted {
		t.Fatal("no baseline row for n with the original folder id")
	}
	if res2 := a.sync(t); len(res2.Plan) != 0 {
		t.Errorf("second cycle not idle: %+v", res2.Plan)
	}
}
