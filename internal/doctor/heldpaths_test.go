package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// S026: a tracked path that is on disk but that the scan did not read — a
// symlink or non-regular file (S013), a file it may not open (S014), anything
// under a directory it may not read (W18-G) — is held by the engine: nothing
// is planned for it and its row is kept. `doctor` must say so, with the scan's
// reason, instead of listing it as a row whose local file is missing. Every
// world also holds a file that is really gone, which must still be missing.

// heldWorld syncs keep.txt and gone.txt plus whatever seed writes, deletes
// gone.txt, then lets arrange turn tracked paths into held ones.
func heldWorld(t *testing.T, seed func(w *world), arrange func(w *world)) *world {
	t.Helper()
	w := newWorld(t)
	w.write(t, "keep.txt", "kept")
	w.write(t, "gone.txt", "really deleted")
	seed(w)
	w.sync(t)
	if err := os.Remove(filepath.Join(w.syncDir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	arrange(w)
	return w
}

// assertHeld checks the report: exactly the want paths are held, each with a
// reason containing its substring, none of them is missing, and gone.txt is.
func assertHeld(t *testing.T, rep *Report, want map[string]string) {
	t.Helper()
	got := map[string]string{}
	for _, h := range rep.HeldLocal {
		got[h.RelPath] = h.Reason
	}
	for p, sub := range want {
		why, ok := got[p]
		if !ok {
			t.Errorf("%s not reported as held; held = %+v", p, rep.HeldLocal)
		} else if !strings.Contains(why, sub) {
			t.Errorf("%s held for %q, want a reason containing %q", p, why, sub)
		}
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			t.Errorf("%s reported as held, want only %v", p, want)
		}
	}
	for _, p := range rep.MissingLocal {
		if _, ok := want[p]; ok {
			t.Errorf("held path %s reported as missing locally", p)
		}
	}
	if len(rep.MissingLocal) != 1 || rep.MissingLocal[0] != "gone.txt" {
		t.Errorf("MissingLocal = %v, want [gone.txt] (the control)", rep.MissingLocal)
	}
	if rep.Healthy() {
		t.Error("a world with held rows reported healthy")
	}
}

// assertRepairUnchanged runs --repair and checks it did what it always did:
// adopted only the pairs present on both sides (keep.txt plus the extra
// adoptable paths), kept every held row byte for byte, trashed nothing on
// Drive, and wrote nothing at a held path.
func assertRepairUnchanged(t *testing.T, w *world, want map[string]string, adoptable int) {
	t.Helper()
	ctx := context.Background()
	before := rowsByPath(t, w)
	trashed := w.fake.TrashCount()
	rep, err := w.doctor().Repair(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Adopted != 1+adoptable {
		t.Errorf("repair adopted %d rows, want %d (only pairs present locally)", rep.Adopted, 1+adoptable)
	}
	after := rowsByPath(t, w)
	if len(after) != len(before) {
		t.Errorf("repair changed the row count: %d -> %d", len(before), len(after))
	}
	for p := range want {
		if after[p] != before[p] {
			t.Errorf("repair touched held row %s: %+v -> %+v", p, before[p], after[p])
		}
		f, err := w.fake.Get(ctx, before[p].DriveFileID)
		if err != nil || f.Trashed {
			t.Errorf("held row %s: Drive item gone after repair (%v, trashed %v)", p, err, f.Trashed)
		}
	}
	if got := w.fake.TrashCount(); got != trashed {
		t.Errorf("repair trashed %d Drive items", got-trashed)
	}
	if _, err := os.Lstat(filepath.Join(w.syncDir, "gone.txt")); !os.IsNotExist(err) {
		t.Errorf("repair re-created the deleted gone.txt: %v", err)
	}
	assertHeld(t, rep, want)
}

func rowsByPath(t *testing.T, w *world) map[string]statedb.Item {
	t.Helper()
	items, err := w.db.AllItems()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]statedb.Item{}
	for _, it := range items {
		out[it.RelPath] = it
	}
	return out
}

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
}

func (w *world) check(t *testing.T) *Report {
	t.Helper()
	rep, err := w.doctor().Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// (a) S013: a tracked file replaced by a symlink.
func TestS026SymlinkedFileIsHeldNotMissing(t *testing.T) {
	w := heldWorld(t, func(w *world) { w.write(t, "a.txt", "a") }, func(w *world) {
		p := filepath.Join(w.syncDir, "a.txt")
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(t.TempDir(), "elsewhere.txt")
		if err := os.WriteFile(target, []byte("outside"), 0o644); err != nil {
			t.Fatal(err)
		}
		symlinkOrSkip(t, target, p)
	})
	want := map[string]string{"a.txt": "symlink; not followed"}
	assertHeld(t, w.check(t), want)
	assertRepairUnchanged(t, w, want, 0)
}

// (b) S013: a tracked folder replaced by a symlink — the folder row and every
// row under it are held, those under it naming the link.
func TestS026SymlinkedFolderIsHeldNotMissing(t *testing.T) {
	w := heldWorld(t, func(w *world) {
		w.write(t, "dir/b.txt", "b")
		w.write(t, "dir/sub/c.txt", "c")
	}, func(w *world) {
		p := filepath.Join(w.syncDir, "dir")
		target := filepath.Join(t.TempDir(), "moved")
		if err := os.Rename(p, target); err != nil {
			t.Fatal(err)
		}
		symlinkOrSkip(t, target, p)
	})
	want := map[string]string{
		"dir":           "symlink; not followed",
		"dir/b.txt":     "under dir (symlink; not followed)",
		"dir/sub":       "under dir (symlink; not followed)",
		"dir/sub/c.txt": "under dir (symlink; not followed)",
	}
	assertHeld(t, w.check(t), want)
	assertRepairUnchanged(t, w, want, 0)
}

// (c) S014: a tracked file made unreadable. It is edited first, so the scan
// cannot trust the baseline hash by stat and has to open it.
func TestS026UnreadableFileIsHeldNotMissing(t *testing.T) {
	w := heldWorld(t, func(w *world) { w.write(t, "doc.txt", "original") }, func(w *world) {
		w.write(t, "doc.txt", "edited, then locked")
		p := filepath.Join(w.syncDir, "doc.txt")
		if err := os.Chmod(p, 0o000); err != nil {
			t.Skipf("cannot chmod: %v", err)
		}
		t.Cleanup(func() { os.Chmod(p, 0o644) })
		if f, err := os.Open(p); err == nil {
			f.Close()
			t.Skip("this environment can read a 0000 file (running as root?)")
		}
	})
	want := map[string]string{"doc.txt": "file could not be read"}
	assertHeld(t, w.check(t), want)
	assertRepairUnchanged(t, w, want, 0)
}

// (d) W18-G: rows under a directory the scan may not read. The directory
// itself is known to exist (the scan says so) and is adopted by --repair.
func TestS026RowsUnderUnreadableDirAreHeldNotMissing(t *testing.T) {
	w := heldWorld(t, func(w *world) {
		w.write(t, "locked/x.txt", "x")
		w.write(t, "locked/sub/y.txt", "y")
	}, func(w *world) {
		p := filepath.Join(w.syncDir, "locked")
		if err := os.Chmod(p, 0o000); err != nil {
			t.Skipf("cannot chmod: %v", err)
		}
		t.Cleanup(func() { os.Chmod(p, 0o755) })
		if _, err := os.ReadDir(p); err == nil {
			t.Skip("this environment can read a 0000 directory (running as root?)")
		}
	})
	want := map[string]string{
		"locked/x.txt":     "under locked (directory could not be read",
		"locked/sub":       "under locked (directory could not be read",
		"locked/sub/y.txt": "under locked (directory could not be read",
	}
	assertHeld(t, w.check(t), want)
	assertRepairUnchanged(t, w, want, 1)
}
