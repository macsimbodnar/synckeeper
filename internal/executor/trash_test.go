package executor

// W13-T2/T3: a directory delete that stands for its whole subtree. One move
// to the bin retires the folder; anything the plan did not reason about
// pushes the action back to item-by-item removal, and a build with no usable
// bin falls back to the quarantine. Nothing here is ever a hard delete.

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
	"github.com/macsimbodnar/synckeeper/internal/trash"
)

// subtreeFixture builds docs/{a.txt,sub/b.txt}, tracks all four rows, and
// returns the collapsed action the planner would emit for it.
type subtreeFixture struct {
	base, syncDir string
	db            *statedb.DB
	x             *Executor
	bin           *trash.Fake
	action        reconcile.Action
}

func newSubtreeFixture(t *testing.T, bin *trash.Fake) *subtreeFixture {
	t.Helper()
	base := t.TempDir()
	syncDir := filepath.Join(base, "sync")
	if err := os.MkdirAll(filepath.Join(syncDir, "docs", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"docs/a.txt": "content a", "docs/sub/b.txt": "content b"}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(syncDir, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := statedb.Open(filepath.Join(base, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	action := reconcile.Action{Type: reconcile.QuarantineLocal, RelPath: "docs", FileID: "id-docs", IsDir: true}
	rows := []statedb.Item{
		{DriveFileID: "id-docs", RelPath: "docs", IsDir: true},
		{DriveFileID: "id-sub", RelPath: "docs/sub", IsDir: true},
	}
	action.Subtree = append(action.Subtree, reconcile.SubtreeEntry{RelPath: "docs/sub", IsDir: true, FileID: "id-sub"})
	for rel, content := range files {
		st, err := os.Stat(filepath.Join(syncDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		id := "id-" + rel
		action.Subtree = append(action.Subtree, reconcile.SubtreeEntry{
			RelPath: rel, FileID: id, Size: st.Size(), MtimeNS: st.ModTime().UnixNano(),
		})
		action.SubtreeFiles++
		rows = append(rows, statedb.Item{DriveFileID: id, RelPath: rel, Size: int64(len(content))})
	}
	if err := db.Tx(func(tx *sql.Tx) error {
		for _, it := range rows {
			if err := statedb.UpsertItem(tx, it); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	return &subtreeFixture{
		base: base, syncDir: syncDir, db: db, bin: bin, action: action,
		x: &Executor{DB: db, Client: driveclient.NewFake(), SyncDir: syncDir,
			QuarantineDir: filepath.Join(base, "quarantine"), RootID: "root",
			Ignore: []string{".DS_Store"}, Trash: bin},
	}
}

func (f *subtreeFixture) apply(t *testing.T) Summary {
	t.Helper()
	sum, err := f.x.Apply(context.Background(), []reconcile.Action{f.action})
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

// T13.1: the folder deleted in Drive leaves ONE restorable entry in the bin,
// with its content intact, and retires every baseline row it covered.
func TestCollapsedDirTrashedAsOneEntry(t *testing.T) {
	f := newSubtreeFixture(t, trash.NewFake(filepath.Join(t.TempDir(), "bin")))
	if sum := f.apply(t); sum.Failed != 0 {
		t.Fatalf("collapsed dir delete failed: %v", sum.Errors)
	}
	if moved := f.bin.Moved(); len(moved) != 1 || moved[0] != "docs" {
		t.Fatalf("bin received %v, want exactly one entry: the folder", moved)
	}
	if _, err := os.Lstat(filepath.Join(f.syncDir, "docs")); !os.IsNotExist(err) {
		t.Error("docs still in the sync dir")
	}
	if got, err := os.ReadFile(filepath.Join(f.bin.Dir, "docs", "sub", "b.txt")); err != nil || string(got) != "content b" {
		t.Errorf("nested file not restorable from the bin: %q, %v", got, err)
	}
	n, err := f.db.ItemCount()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d baseline rows survived the collapsed delete, want 0", n)
	}
	if n, _ := f.db.PendingOpCount(); n != 0 {
		t.Errorf("%d journal rows left after a clean run", n)
	}
}

// T13.6: a file that appears after the scan is not the plan's to move. The
// collapse is refused, the covered files are rescued one by one, and the
// stranger stays exactly where the user put it — with the op failed, so the
// next cycle replans against what is really there.
func TestCollapsedDirRefusesUnexpectedSurvivor(t *testing.T) {
	f := newSubtreeFixture(t, trash.NewFake(filepath.Join(t.TempDir(), "bin")))
	stranger := filepath.Join(f.syncDir, "docs", "surprise.txt")
	if err := os.WriteFile(stranger, []byte("mine!"), 0o644); err != nil {
		t.Fatal(err)
	}

	sum := f.apply(t)
	if sum.Failed != 1 {
		t.Fatalf("summary = %+v, want the action to fail on the survivor", sum)
	}
	if got, err := os.ReadFile(stranger); err != nil || string(got) != "mine!" {
		t.Errorf("the file the plan never saw was touched: %q, %v", got, err)
	}
	if moved := f.bin.Moved(); len(moved) != 2 {
		t.Errorf("bin received %v, want the two covered files rescued item by item", moved)
	}
	if _, err := os.Lstat(filepath.Join(f.syncDir, "docs")); err != nil {
		t.Error("the directory holding the survivor must stay")
	}
	if n, _ := f.db.ItemCount(); n == 0 {
		t.Error("rows were retired even though the action failed")
	}
}

// Edit beats delete, inside a collapsed subtree too (§4.2, R13): a covered
// file edited between scan and execution refuses, and the whole folder move
// with it — the edit is still on disk when the next cycle plans.
func TestCollapsedDirRefusesEditedFile(t *testing.T) {
	f := newSubtreeFixture(t, trash.NewFake(filepath.Join(t.TempDir(), "bin")))
	edited := filepath.Join(f.syncDir, "docs", "a.txt")
	if err := os.WriteFile(edited, []byte("edited after the scan"), 0o644); err != nil {
		t.Fatal(err)
	}

	if sum := f.apply(t); sum.Failed != 1 {
		t.Fatalf("summary = %+v, want the action to fail on the edited file", sum)
	}
	if got, err := os.ReadFile(edited); err != nil || string(got) != "edited after the scan" {
		t.Errorf("the mid-cycle edit was rescued out from under the user: %q, %v", got, err)
	}
	for _, name := range f.bin.Moved() {
		if name == "a.txt" || name == "docs" {
			t.Errorf("bin received %q, want the edited file left alone", name)
		}
	}
}

// T13.4: no usable bin (a CGO_ENABLED=0 darwin build, Windows, a broken
// trash) falls back to the quarantine, item by item, exactly as before W13.
// Invariant 3 never degrades to a hard delete.
func TestCollapsedDirWithoutTrashFallsBackToQuarantine(t *testing.T) {
	for _, tc := range []struct {
		name string
		bin  *trash.Fake
	}{
		{"unavailable", &trash.Fake{Unavailable: true}},
		{"refuses the move", &trash.Fake{Dir: t.TempDir(), MoveErr: os.ErrPermission}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSubtreeFixture(t, tc.bin)
			if sum := f.apply(t); sum.Failed != 0 {
				t.Fatalf("quarantine fallback failed: %v", sum.Errors)
			}
			if _, err := os.Lstat(filepath.Join(f.syncDir, "docs")); !os.IsNotExist(err) {
				t.Error("docs still in the sync dir")
			}
			day := time.Now().Format("2006-01-02")
			for rel, want := range map[string]string{"docs/a.txt": "content a", "docs/sub/b.txt": "content b"} {
				p := filepath.Join(f.base, "quarantine", day, filepath.FromSlash(rel))
				if got, err := os.ReadFile(p); err != nil || string(got) != want {
					t.Errorf("%s not rescued to the quarantine: %q, %v", rel, got, err)
				}
			}
			if n, _ := f.db.ItemCount(); n != 0 {
				t.Errorf("%d baseline rows survived the fallback delete, want 0", n)
			}
		})
	}
}

// The bin is only asked for content the plan pinned: an ignored leftover
// inside a collapsed folder travels with it (R20) instead of blocking it.
func TestCollapsedDirCarriesIgnoredLeftovers(t *testing.T) {
	f := newSubtreeFixture(t, trash.NewFake(filepath.Join(t.TempDir(), "bin")))
	if err := os.WriteFile(filepath.Join(f.syncDir, "docs", "sub", ".DS_Store"), []byte("finder"), 0o644); err != nil {
		t.Fatal(err)
	}
	if sum := f.apply(t); sum.Failed != 0 {
		t.Fatalf("an ignored leftover must not block the folder: %v", sum.Errors)
	}
	if _, err := os.Stat(filepath.Join(f.bin.Dir, "docs", "sub", ".DS_Store")); err != nil {
		t.Errorf("ignored leftover did not travel with the folder: %v", err)
	}
}
