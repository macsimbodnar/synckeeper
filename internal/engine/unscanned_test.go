package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// S013 (audit 2026-11-11 F07) — a tracked path that is still on disk but the
// scan does not read (a symlink, never followed; a non-regular file) is not a
// local deletion. Its baseline rows are held harmless until it is a plain
// file or folder again: nothing trashed on Drive, nothing written through or
// over the link, no row forgotten. F07 itself is in audit_2026_11_11_test.go.

// replaceWithSymlink moves rel out of m's sync dir and links it back; returns
// where the content now lives.
func replaceWithSymlink(t *testing.T, m *machine, rel string) string {
	t.Helper()
	p := filepath.Join(m.dir, filepath.FromSlash(rel))
	elsewhere := filepath.Join(t.TempDir(), filepath.Base(p))
	if err := os.Rename(p, elsewhere); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, p); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	return elsewhere
}

// assertStillSymlink fails unless rel in m's sync dir is still the user's link.
func assertStillSymlink(t *testing.T, m *machine, rel string) {
	t.Helper()
	info, err := os.Lstat(filepath.Join(m.dir, filepath.FromSlash(rel)))
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("[%s] %s is no longer the user's symlink (err %v)", m.name, rel, err)
	}
}

// actionsOn returns the plan's actions that name rel or a path under it.
func actionsOn(plan []reconcile.Action, rel string) []reconcile.Action {
	var out []reconcile.Action
	under := func(p string) bool { return p == rel || (len(p) > len(rel) && p[:len(rel)+1] == rel+"/") }
	for _, a := range plan {
		if under(a.RelPath) || (a.NewRelPath != "" && under(a.NewRelPath)) {
			out = append(out, a)
		}
	}
	return out
}

func skipReported(skips []reconcile.Skip, rel string) bool {
	for _, s := range skips {
		if s.RelPath == rel {
			return true
		}
	}
	return false
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// The file flavour of F07: a tracked file moved away and linked back. Then
// Drive edits it — the download must not replace the link (or write into its
// target), and the row must survive for when the link becomes a file again.
func TestS013TrackedFileReplacedBySymlinkIsHeld(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "doc.txt", "original")
	a.write(t, "keep.txt", "k")
	a.sync(t)
	b.sync(t)

	elsewhere := replaceWithSymlink(t, a, "doc.txt")
	res := a.sync(t)
	if got := actionsOn(res.Plan, "doc.txt"); len(got) != 0 {
		t.Fatalf("a tracked file replaced by a symlink was acted on: %+v", got)
	}
	if !skipReported(res.Skips, "doc.txt") {
		t.Errorf("the symlink was not reported: %+v", res.Skips)
	}
	b.sync(t)
	if got := driveTreeOf(t, fake, root)["doc.txt"]; got != "original" {
		t.Fatalf("doc.txt on Drive = %q, want it left alone", got)
	}
	if got := b.listTree(t)["doc.txt"]; got != "original" {
		t.Fatalf("doc.txt on machine b = %q, want it left alone", got)
	}

	// Drive changes it while it is held.
	b.write(t, "doc.txt", "edited on b")
	b.sync(t)
	res = a.sync(t)
	if got := actionsOn(res.Plan, "doc.txt"); len(got) != 0 {
		t.Fatalf("a Drive edit was applied over the user's symlink: %+v", got)
	}
	assertStillSymlink(t, a, "doc.txt")
	if got := readFile(t, elsewhere); got != "original" {
		t.Fatalf("the link's target was written through: %q", got)
	}

	// Link turned back into the file it was: the Drive edit arrives as an
	// ordinary download, no conflict copy, nothing re-uploaded.
	a.remove(t, "doc.txt")
	if err := os.Rename(elsewhere, filepath.Join(a.dir, "doc.txt")); err != nil {
		t.Fatal(err)
	}
	res = a.sync(t)
	if n := auditCount(res.Plan, reconcile.Upload); n != 0 {
		t.Errorf("restoring the file uploaded %d files: %+v", n, res.Plan)
	}
	if got := a.read(t, "doc.txt"); got != "edited on b" {
		t.Fatalf("after the link became a file again, doc.txt = %q, want Drive's edit", got)
	}
	b.sync(t)
	assertConverged(t, a, b)
}

// A tracked folder moved to another disk and linked back, while another
// machine edits, adds and deletes inside it on Drive. Nothing reaches the
// link's target; once the link is a folder again, every Drive change lands as
// the ordinary three-way decision (no row was forgotten or overwritten).
func TestS013FolderReplacedBySymlinkWhileDriveChangesInside(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "photos/1.jpg", "one")
	a.write(t, "photos/2.jpg", "two")
	a.write(t, "keep.txt", "k")
	a.sync(t)
	b.sync(t)

	elsewhere := replaceWithSymlink(t, a, "photos")
	b.write(t, "photos/1.jpg", "one, edited on b")
	b.write(t, "photos/3.jpg", "three, new on b")
	b.write(t, "photos/sub/4.jpg", "four, in a new folder on b")
	b.remove(t, "photos/2.jpg")
	b.sync(t)

	// syncRaw: the W16-E4 check (every baseline row mirrored) does not hold
	// here by design — photos/2.jpg is gone from Drive, and its row is kept
	// rather than forgotten, so the deletion reaches a once photos is back.
	res := a.syncRaw(t)
	if got := actionsOn(res.Plan, "photos"); len(got) != 0 {
		t.Fatalf("Drive changes under a symlinked folder were acted on: %+v", got)
	}
	for _, rel := range []string{"photos", "photos/3.jpg", "photos/sub"} {
		if !skipReported(res.Skips, rel) {
			t.Errorf("%s not reported as held: %+v", rel, res.Skips)
		}
	}
	assertStillSymlink(t, a, "photos")
	entries, err := os.ReadDir(elsewhere)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || readFile(t, filepath.Join(elsewhere, "1.jpg")) != "one" {
		t.Fatalf("the link's target was written through: %d entries, 1.jpg = %q",
			len(entries), readFile(t, filepath.Join(elsewhere, "1.jpg")))
	}
	if got := driveTreeOf(t, fake, root); len(got) != 4 || got["photos/1.jpg"] != "one, edited on b" {
		t.Fatalf("Drive was changed by the held machine: %v", got)
	}

	// The link becomes a folder again.
	a.remove(t, "photos")
	if err := os.Rename(elsewhere, filepath.Join(a.dir, "photos")); err != nil {
		t.Fatal(err)
	}
	res = a.sync(t)
	if n := auditCount(res.Plan, reconcile.Upload); n != 0 {
		t.Errorf("restoring the folder uploaded %d files: %+v", n, res.Plan)
	}
	b.sync(t)
	assertConverged(t, a, b)
	want := map[string]string{
		"keep.txt": "k", "photos/1.jpg": "one, edited on b",
		"photos/3.jpg": "three, new on b", "photos/sub/4.jpg": "four, in a new folder on b",
	}
	got := a.listTree(t)
	if len(got) != len(want) {
		t.Fatalf("after restoring the folder, a holds %v, want %v", sortedKeys(got), sortedKeys(want))
	}
	for p, c := range want {
		if got[p] != c {
			t.Errorf("%s = %q, want %q", p, got[p], c)
		}
	}
	if raw, err := os.ReadFile(a.binPath("photos/2.jpg")); err != nil || string(raw) != "two" {
		t.Errorf("Drive's deletion of photos/2.jpg did not reach a's bin once the folder was back")
	}
}

// The way back: the link replaced by a real folder holding the same content,
// written fresh (new mtimes). It must converge with no transfer at all — the
// held rows are still the baseline, so the content is recognised, not
// uploaded as new beside the old.
func TestS013SymlinkTurnedBackIntoFolderNeedsNoTransfer(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "photos/1.jpg", "one")
	a.write(t, "photos/deep/2.jpg", "two")
	a.write(t, "keep.txt", "k")
	a.sync(t)
	b.sync(t)

	replaceWithSymlink(t, a, "photos")
	a.sync(t)
	a.sync(t) // a standing condition: a second cycle must not decide differently

	a.remove(t, "photos")
	a.write(t, "photos/1.jpg", "one")
	a.write(t, "photos/deep/2.jpg", "two")
	res := a.sync(t)
	for _, act := range res.Plan {
		if act.Type != reconcile.Record {
			t.Errorf("unchanged content cost a %s: %+v", act.Type, act)
		}
	}
	b.sync(t)
	assertConverged(t, a, b)
	if got := driveTreeOf(t, fake, root); len(got) != 3 {
		t.Fatalf("Drive holds %v, want the three original files and no duplicate", sortedKeys(got))
	}
	if n := auditDriveFiles(t, fake, root); n != 3 {
		t.Fatalf("Drive holds %d files, want 3 (a same-name duplicate would hide in a path-keyed view)", n)
	}
}

// Review of S013, hole 1, the protected route: the folder holding a held
// link is renamed locally and a file beside the link pairs the rename. The
// rows follow it to b/link and stay held: nothing trashed on Drive. Turning
// the link back into a folder then needs no upload.
func TestS013LinkParentRenamedWithASiblingKeepsDriveAndConverges(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "a/link/1.jpg", "one")
	a.write(t, "a/link/2.jpg", "two")
	a.write(t, "a/note.txt", "beside the link")
	a.sync(t)
	b.sync(t)

	elsewhere := replaceWithSymlink(t, a, "a/link")
	a.sync(t)
	a.rename(t, "a", "b")
	res := a.sync(t)
	if n := countDeleteClass(res.Plan); n != 0 {
		t.Fatalf("renaming the link's parent planned %d deletes: %+v", n, res.Plan)
	}
	if !skipReported(res.Skips, "a/link/1.jpg") {
		t.Errorf("the held rows were not reported: %+v", res.Skips)
	}
	b.sync(t)
	if got := driveTreeOf(t, fake, root); got["b/link/1.jpg"] != "one" || got["b/link/2.jpg"] != "two" {
		t.Fatalf("Drive lost the content behind the link: %v", got)
	}
	if got := b.listTree(t); len(got) != 3 {
		t.Fatalf("machine b holds %v, want 3 files under b/", sortedKeys(got))
	}

	a.remove(t, "b/link")
	if err := os.Rename(elsewhere, filepath.Join(a.dir, "b", "link")); err != nil {
		t.Fatal(err)
	}
	res = a.sync(t)
	if n := auditCount(res.Plan, reconcile.Upload); n != 0 {
		t.Errorf("turning the link back into a folder uploaded %d files: %+v", n, res.Plan)
	}
	b.sync(t)
	assertConverged(t, a, b)
	if n := auditDriveFiles(t, fake, root); n != 3 {
		t.Fatalf("Drive holds %d files, want 3 (no duplicate)", n)
	}
}

// Review of S013, hole 2: on a case-folding disk an untracked symlink
// "photos" and a new Drive folder "Photos" are one path. Nothing may be
// created or downloaded through the link into the folder it points at.
func TestS013CaseFoldedDriveFolderNeverLandsBehindASymlink(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	if !a.eng.caseInsensitive() {
		t.Skip("needs a case-folding filesystem")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(a.dir, "photos")); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	b.write(t, "Photos/x.jpg", "from b")
	b.sync(t)

	res := a.sync(t)
	if got := actionsOn(res.Plan, "Photos"); len(got) != 0 {
		t.Fatalf("Drive's Photos was planned through the symlink photos: %+v", got)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("the link's target was written: %v (err %v)", entries, err)
	}
	if !skipReported(res.Skips, "Photos/x.jpg") {
		t.Errorf("the held Drive item was not reported: %+v", res.Skips)
	}
}

// Round-3 review, B, end to end: photos is a symlink on a, so photos/x is
// held. b moves x on Drive into folder X; a deletes X locally. X/x must not
// go to Drive's bin with the folder, and once photos is a folder again a
// must follow b's move, not bin its own copy.
func TestS013HeldFileMovedIntoAFolderDeletedHereSurvives(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "photos/x", "precious")
	a.write(t, "X/y", "y")
	a.sync(t)
	b.sync(t)

	elsewhere := replaceWithSymlink(t, a, "photos")
	a.sync(t)
	b.rename(t, "photos/x", "X/x")
	b.sync(t)
	a.remove(t, "X")
	res := a.sync(t)
	for _, act := range res.Plan {
		if act.Type == reconcile.TrashRemote && act.RelPath == "X" {
			t.Fatalf("folder X trashed on Drive with the held file in it: %+v", res.Plan)
		}
	}
	if got := driveTreeOf(t, fake, root)["X/x"]; got != "precious" {
		t.Fatalf("X/x on Drive = %q, want it kept", got)
	}
	b.sync(t)
	if got := b.listTree(t)["X/x"]; got != "precious" {
		t.Fatalf("X/x on b = %q, want it kept", got)
	}

	a.remove(t, "photos")
	if err := os.Rename(elsewhere, filepath.Join(a.dir, "photos")); err != nil {
		t.Fatal(err)
	}
	res = a.sync(t)
	if n := countDeleteClass(res.Plan); n != 0 {
		t.Fatalf("restoring photos planned %d deletes: %+v", n, res.Plan)
	}
	b.sync(t)
	assertConverged(t, a, b)
	if got := a.listTree(t)["X/x"]; got != "precious" {
		t.Fatalf("a holds %v, want X/x moved into place", sortedKeys(a.listTree(t)))
	}
}
