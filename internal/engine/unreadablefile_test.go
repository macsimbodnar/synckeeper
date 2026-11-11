package engine

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// S014 (audit 2026-11-11 F08) — a file the scan cannot open is held like an
// S013 path: a reported skip, nothing planned at its path on either side, its
// baseline row kept; the rest of the tree syncs. F08 itself lives in
// audit_2026_11_11_test.go.

// lockLocal makes rel in m's sync dir unreadable, or skips the test where
// that cannot be arranged (root, Windows, a filesystem ignoring the mode).
func lockLocal(t *testing.T, m *machine, rel string) {
	t.Helper()
	p := filepath.Join(m.dir, filepath.FromSlash(rel))
	if err := os.Chmod(p, 0o000); err != nil {
		t.Skipf("cannot chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(p, 0o644) })
	if f, err := os.Open(p); err == nil {
		f.Close()
		t.Skip("this environment can read a 0000 file (running as root?)")
	}
}

func unlockLocal(t *testing.T, m *machine, rel string) {
	t.Helper()
	if err := os.Chmod(filepath.Join(m.dir, filepath.FromSlash(rel)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A tracked file edited locally and then made unreadable (the scan must hash
// it): held, not read as deleted. A Drive edit meanwhile does not land over
// it. Readable again → the ordinary three-way decision: both changed, so the
// local edit is kept as the conflict copy and nothing is lost.
func TestS014TrackedUnreadableFileIsHeld(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "doc.txt", "original")
	a.write(t, "keep.txt", "k")
	a.sync(t)
	b.sync(t)

	a.write(t, "doc.txt", "edited on a")
	lockLocal(t, a, "doc.txt")
	a.write(t, "new.txt", "written meanwhile")
	res := a.sync(t)
	if got := actionsOn(res.Plan, "doc.txt"); len(got) != 0 {
		t.Fatalf("an unreadable tracked file was acted on: %+v", got)
	}
	if !skipReported(res.Skips, "doc.txt") {
		t.Errorf("the unreadable file was not reported: %+v", res.Skips)
	}
	if got := driveTreeOf(t, fake, root); got["doc.txt"] != "original" || got["new.txt"] != "written meanwhile" {
		t.Fatalf("Drive = %v, want doc.txt untouched and new.txt synced", got)
	}

	b.write(t, "doc.txt", "edited on b")
	b.sync(t)
	res = a.sync(t)
	if got := actionsOn(res.Plan, "doc.txt"); len(got) != 0 {
		t.Fatalf("a Drive edit was planned over the unreadable file: %+v", got)
	}

	unlockLocal(t, a, "doc.txt")
	a.sync(t)
	b.sync(t)
	assertConverged(t, a, b)
	if got := a.read(t, "doc.txt"); got != "edited on b" {
		t.Fatalf("doc.txt = %q, want Drive's edit at the plain name", got)
	}
	if n := countConflictCopies(t, a.dir); n != 1 {
		t.Fatalf("want a's own edit kept as one conflict copy, got %d", n)
	}
}

// Unreadable while unchanged in content (only its mtime moved, so the scan
// tries to hash it): held; readable again → a record, no transfer.
func TestS014UnreadableFileReadableAgainNeedsNoTransfer(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	a.write(t, "doc.txt", "same bytes")
	a.sync(t)

	p := filepath.Join(a.dir, "doc.txt")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	lockLocal(t, a, "doc.txt")
	res := a.sync(t)
	if got := actionsOn(res.Plan, "doc.txt"); len(got) != 0 {
		t.Fatalf("held file acted on: %+v", got)
	}

	unlockLocal(t, a, "doc.txt")
	res = a.sync(t)
	for _, act := range res.Plan {
		if act.Type != reconcile.Record {
			t.Errorf("readable again with unchanged content cost a %s: %+v", act.Type, act)
		}
	}
	// The new mtime must be recorded — an empty plan would leave the
	// baseline stale and rehash the file every cycle.
	if len(res.Plan) != 1 || res.Plan[0].RelPath != "doc.txt" {
		t.Fatalf("want exactly one record for doc.txt, got %+v", res.Plan)
	}
}

// Not tracked yet: reported, not uploaded, and a Drive item arriving at the
// same path does not overwrite it. Readable again → both-new, different
// content: the ordinary conflict, both versions kept.
func TestS014UntrackedUnreadableFileIsNotOverwrittenByDrive(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "secret.txt", "mine")
	lockLocal(t, a, "secret.txt")
	b.write(t, "secret.txt", "from b")
	b.sync(t)

	res := a.sync(t)
	if got := actionsOn(res.Plan, "secret.txt"); len(got) != 0 {
		t.Fatalf("Drive's item was planned over the unreadable file: %+v", got)
	}
	if !skipReported(res.Skips, "secret.txt") {
		t.Errorf("not reported: %+v", res.Skips)
	}
	if got := driveTreeOf(t, fake, root)["secret.txt"]; got != "from b" {
		t.Fatalf("Drive secret.txt = %q, want b's", got)
	}

	unlockLocal(t, a, "secret.txt")
	if got := a.read(t, "secret.txt"); got != "mine" {
		t.Fatalf("the unreadable file was overwritten: %q", got)
	}
	a.sync(t)
	b.sync(t)
	assertConverged(t, a, b)
	if n := countConflictCopies(t, a.dir); n != 1 || a.read(t, "secret.txt") != "from b" {
		t.Fatalf("want Drive's at the plain name and a's as one conflict copy: %v", sortedKeys(a.listTree(t)))
	}
}

// The daemon has no channel for skips (S003), and a file that silently stops
// syncing is worse than the `last error` wedge it replaces: like an
// unreadable folder (W18-G), it is warned about once per occurrence, not once
// per cycle, in words true for a file.
func TestS014UnreadableFileIsWarnedAboutOnce(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	a.write(t, "secret.txt", "mine")
	lockLocal(t, a, "secret.txt")

	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(old)

	a.sync(t)
	a.sync(t) // the same standing condition, next cycle
	var warns []string
	for _, l := range strings.Split(logs.String(), "\n") {
		if strings.Contains(l, "level=WARN") && strings.Contains(l, "secret.txt") {
			warns = append(warns, l)
		}
	}
	if len(warns) != 1 {
		t.Fatalf("want one warning naming secret.txt over two cycles, got %d:\n%s", len(warns), logs.String())
	}
	// The message only: the reason carries the path, and macOS temp dirs
	// live under /var/folders.
	msg, _, _ := strings.Cut(warns[0], " rel_path=")
	if strings.Contains(msg, "folder") || !strings.Contains(msg, "a file") {
		t.Errorf("want a message about a file, got: %s", msg)
	}
}

// Re-arms like the folder warning: once the file is readable again (no skip
// that cycle), a later recurrence is warned about again.
func TestS014UnreadableFileWarningReArms(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(old)

	e := &Engine{}
	locked := []reconcile.Skip{{RelPath: "secret.txt", Unscanned: true, UnreadableFile: true, Reason: "permission denied"}}
	e.warnUnreadable(locked)
	e.warnUnreadable(locked)
	if got := strings.Count(logs.String(), "a file in the sync dir cannot be read"); got != 1 {
		t.Fatalf("warned %d times about one standing condition, want 1:\n%s", got, logs.String())
	}
	e.warnUnreadable(nil) // readable again
	e.warnUnreadable(locked)
	if got := strings.Count(logs.String(), "a file in the sync dir cannot be read"); got != 2 {
		t.Fatalf("warned %d times, want 2 — the warning must re-arm:\n%s", got, logs.String())
	}
	if expandUnreadable(nil, locked) != nil {
		t.Fatal("a file skip triggered W18-G's directory expansion")
	}
}

// Round-2 review, 1: a tracked file renamed locally and unreadable (a
// root-owned file renamed in Finder). `mv` keeps size and mtime; the new name
// has no baseline row, so the scan must hash it and cannot. The old row must
// not read as a local deletion. Readable again → the rename it was.
func TestS014RenamedUnreadableFileIsNotTrashed(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "x.txt", "precious")
	a.sync(t)
	b.sync(t)

	a.rename(t, "x.txt", "y.txt")
	lockLocal(t, a, "y.txt")
	res := a.sync(t)
	if n := countDeleteClass(res.Plan); n != 0 {
		t.Fatalf("a renamed unreadable file was read as deleted: %+v", res.Plan)
	}
	if !skipReported(res.Skips, "x.txt") {
		t.Errorf("the held row was not reported: %+v", res.Skips)
	}
	b.sync(t)
	if got := b.listTree(t)["x.txt"]; got != "precious" {
		t.Fatalf("b lost x.txt: %v", sortedKeys(b.listTree(t)))
	}

	unlockLocal(t, a, "y.txt")
	res = a.sync(t)
	if n := auditCount(res.Plan, reconcile.MoveRemote); n != 1 || auditCount(res.Plan, reconcile.Upload) != 0 {
		t.Fatalf("readable again: want one move_remote and no upload, got %+v", res.Plan)
	}
	b.sync(t)
	assertConverged(t, a, b)
	if got := b.listTree(t); len(got) != 1 || got["y.txt"] != "precious" {
		t.Fatalf("b holds %v, want only y.txt", sortedKeys(got))
	}
	if n := auditDriveFiles(t, fake, root); n != 1 {
		t.Fatalf("Drive holds %d files, want 1", n)
	}
}

// Folder form: d/x.txt, `mv d e`, e/x.txt unreadable, nothing readable
// beside it. Folder d must not be trashed with the file inside it.
func TestS014RenamedFolderWithOnlyAnUnreadableFileIsNotTrashed(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "d/x.txt", "precious")
	a.sync(t)
	b.sync(t)

	a.rename(t, "d", "e")
	lockLocal(t, a, "e/x.txt")
	res := a.sync(t)
	if n := countDeleteClass(res.Plan); n != 0 {
		t.Fatalf("the renamed folder was read as deleted: %+v", res.Plan)
	}
	b.sync(t)
	if got := b.listTree(t)["d/x.txt"]; got != "precious" {
		t.Fatalf("b lost d/x.txt: %v", sortedKeys(b.listTree(t)))
	}

	unlockLocal(t, a, "e/x.txt")
	res = a.sync(t)
	if n := auditCount(res.Plan, reconcile.Upload); n != 0 {
		t.Fatalf("readable again uploaded %d files: %+v", n, res.Plan)
	}
	b.sync(t)
	assertConverged(t, a, b)
	if got := b.listTree(t); len(got) != 1 || got["e/x.txt"] != "precious" {
		t.Fatalf("b holds %v, want only e/x.txt", sortedKeys(got))
	}
	if n := auditDriveFiles(t, fake, root); n != 1 {
		t.Fatalf("Drive holds %d files, want 1", n)
	}
}

// The match is identity (size and mtime), not a name: an unrelated
// unreadable file does not hold a real deletion.
func TestS014UnrelatedUnreadableFileHoldsNoDeletion(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	a.write(t, "x.txt", "precious")
	a.sync(t)

	a.remove(t, "x.txt")
	a.write(t, "z.txt", "something else entirely")
	lockLocal(t, a, "z.txt")
	res := a.sync(t)
	if n := auditCount(res.Plan, reconcile.TrashRemote); n != 1 {
		t.Fatalf("a real deletion beside an unrelated unreadable file was held: %+v", res.Plan)
	}
}

// Round-2 review, 2 (W18-G): a folder holding an unreadable SUBDIRECTORY is
// renamed locally; a readable sibling lets the rename be detected. The rows
// under the subdirectory must be held where the rename puts them, not read
// as deleted.
func TestS014UnreadableSubdirUnderARenamedFolderIsHeld(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "p/a.txt", "a")
	a.write(t, "p/sub/b.txt", "precious")
	a.sync(t)
	b.sync(t)

	a.rename(t, "p", "q")
	sub := filepath.Join(a.dir, "q", "sub")
	if err := os.Chmod(sub, 0o000); err != nil {
		t.Skipf("cannot chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(sub, 0o755) })
	if _, err := os.ReadDir(sub); err == nil {
		t.Skip("this environment can read a 0000 directory (running as root?)")
	}
	res := a.sync(t)
	if n := countDeleteClass(res.Plan); n != 0 {
		t.Fatalf("rows under the unreadable subdirectory read as deleted: %+v", res.Plan)
	}
	b.sync(t)
	if got := b.listTree(t); got["q/sub/b.txt"] != "precious" || got["q/a.txt"] != "a" {
		t.Fatalf("b holds %v, want the rename carried with q/sub/b.txt", sortedKeys(got))
	}

	if err := os.Chmod(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	res = a.sync(t)
	if len(res.Plan) != 0 {
		t.Fatalf("readable again, want nothing to do, got %+v", res.Plan)
	}
	b.sync(t)
	assertConverged(t, a, b)
}
