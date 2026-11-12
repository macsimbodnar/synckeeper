package executor

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// S035 — crash recovery commits what an interrupted run did on disk but never
// recorded: a MoveLocal's rename (file or folder) or a download's replace,
// on the disk's evidence, and on the journal's "applied" mark where the disk
// alone cannot tell (a new folder took the old name). Nothing else.

type owedWorld struct {
	t       *testing.T
	db      *statedb.DB
	syncDir string
}

func newOwedWorld(t *testing.T) *owedWorld {
	t.Helper()
	base := t.TempDir()
	w := &owedWorld{t: t, syncDir: filepath.Join(base, "sync")}
	if err := os.MkdirAll(w.syncDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := statedb.Open(filepath.Join(base, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	w.db = db
	return w
}

func (w *owedWorld) file(rel, content string, mtime time.Time) os.FileInfo {
	w.t.Helper()
	p := filepath.Join(w.syncDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		w.t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		w.t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		w.t.Fatal(err)
	}
	return info
}

func (w *owedWorld) row(it statedb.Item) {
	w.t.Helper()
	if err := w.db.Tx(func(tx *sql.Tx) error { return statedb.UpsertItem(tx, it) }); err != nil {
		w.t.Fatal(err)
	}
}

// op journals a as the previous run did and leaves it in state.
func (w *owedWorld) op(a reconcile.Action, state string) {
	w.t.Helper()
	payload, err := json.Marshal(a)
	if err != nil {
		w.t.Fatal(err)
	}
	ids, err := w.db.AddOps([]statedb.PendingOp{{Type: string(a.Type), RelPath: a.RelPath, FileID: a.FileID, Payload: string(payload)}})
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.db.SetOpState(ids[0], state); err != nil {
		w.t.Fatal(err)
	}
}

func (w *owedWorld) recover() map[string]statedb.Item {
	w.t.Helper()
	stale, err := w.db.StaleOps()
	if err != nil {
		w.t.Fatal(err)
	}
	if err := finishOwedCommits(w.db, w.syncDir, stale); err != nil {
		w.t.Fatal(err)
	}
	items, err := w.db.AllItems()
	if err != nil {
		w.t.Fatal(err)
	}
	out := map[string]statedb.Item{}
	for _, it := range items {
		out[it.RelPath] = it
	}
	return out
}

func md5hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

var owedMtime = time.Unix(1_700_000_000, 0)

// The folder rename is on disk, the old name gone: committed, children too.
func TestS035RecoveryCommitsAFolderRename(t *testing.T) {
	w := newOwedWorld(t)
	w.row(statedb.Item{DriveFileID: "FD", RelPath: "D", IsDir: true})
	w.row(statedb.Item{DriveFileID: "F", RelPath: "D/f.txt", Size: 1, ContentMD5: md5hex("x"), LocalMtimeNS: owedMtime.UnixNano()})
	w.file("N/f.txt", "x", owedMtime)
	w.op(reconcile.Action{Type: reconcile.MoveLocal, RelPath: "D", NewRelPath: "N", IsDir: true}, statedb.OpInProgress)

	rows := w.recover()
	if rows["N"].DriveFileID != "FD" || rows["N/f.txt"].DriveFileID != "F" {
		t.Fatalf("rows not re-homed at N: %v", rows)
	}
}

// A new folder took the old name after the rename: the disk alone shows two
// folders; the journal's applied mark says the rename ran.
func TestS035RecoveryCommitsAFolderRenameWhenTheNameIsTakenAgain(t *testing.T) {
	for _, tc := range []struct {
		state string
		want  string
	}{{statedb.OpApplied, "N"}, {statedb.OpInProgress, "D"}} {
		w := newOwedWorld(t)
		w.row(statedb.Item{DriveFileID: "FD", RelPath: "D", IsDir: true})
		w.file("N/f.txt", "x", owedMtime)
		w.file("D/f.txt", "new", owedMtime) // a new folder and file at the old name
		w.op(reconcile.Action{Type: reconcile.MoveLocal, RelPath: "D", NewRelPath: "N", IsDir: true}, tc.state)
		rows := w.recover()
		if rows[tc.want].DriveFileID != "FD" {
			t.Errorf("state %s: folder row at %v, want at %s", tc.state, rows, tc.want)
		}
	}
}

// A file rename: committed only for the row's own file (size + mtime, or its
// content), never for whatever else sits at the destination.
func TestS035RecoveryCommitsAFileRenameOnlyForTheRowsFile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		mtime   time.Time
		want    string
	}{
		{"same stat", "x", owedMtime, "g.txt"},
		{"same bytes, mtime touched", "x", owedMtime.Add(time.Hour), "g.txt"},
		{"other bytes", "y", owedMtime.Add(time.Hour), "f.txt"},
	} {
		w := newOwedWorld(t)
		w.row(statedb.Item{DriveFileID: "F", RelPath: "f.txt", Size: 1, ContentMD5: md5hex("x"), LocalMtimeNS: owedMtime.UnixNano()})
		w.file("g.txt", tc.content, tc.mtime)
		w.op(reconcile.Action{Type: reconcile.MoveLocal, RelPath: "f.txt", NewRelPath: "g.txt", FileID: "F"}, statedb.OpApplied)
		rows := w.recover()
		if rows[tc.want].DriveFileID != "F" {
			t.Errorf("%s: row at %v, want at %s", tc.name, rows, tc.want)
		}
	}
}

// The rename never ran (the move failed, or was refused): source still there,
// no applied mark — nothing is committed.
func TestS035RecoveryLeavesAMoveThatNeverRan(t *testing.T) {
	w := newOwedWorld(t)
	w.row(statedb.Item{DriveFileID: "F", RelPath: "f.txt", Size: 1, ContentMD5: md5hex("x"), LocalMtimeNS: owedMtime.UnixNano()})
	w.file("f.txt", "x", owedMtime)
	w.file("g.txt", "x", owedMtime) // an identical occupant the guard refused to clobber
	w.op(reconcile.Action{Type: reconcile.MoveLocal, RelPath: "f.txt", NewRelPath: "g.txt", FileID: "F"}, statedb.OpInProgress)
	if rows := w.recover(); rows["f.txt"].DriveFileID != "F" {
		t.Fatalf("row moved for a rename that never ran: %v", rows)
	}
}

// A destination another row holds: rows are unique per path, nothing done.
func TestS035RecoveryNeverCommitsOntoAnotherRow(t *testing.T) {
	w := newOwedWorld(t)
	w.row(statedb.Item{DriveFileID: "F", RelPath: "f.txt", Size: 1, ContentMD5: md5hex("x"), LocalMtimeNS: owedMtime.UnixNano()})
	w.row(statedb.Item{DriveFileID: "G", RelPath: "g.txt", Size: 1, ContentMD5: md5hex("z"), LocalMtimeNS: owedMtime.UnixNano()})
	w.file("g.txt", "x", owedMtime)
	w.op(reconcile.Action{Type: reconcile.MoveLocal, RelPath: "f.txt", NewRelPath: "g.txt", FileID: "F"}, statedb.OpApplied)
	if rows := w.recover(); rows["f.txt"].DriveFileID != "F" || rows["g.txt"].DriveFileID != "G" {
		t.Fatalf("rows changed: %v", rows)
	}
}

// A download whose replace is done: committed when the target holds its bytes.
func TestS035RecoveryCommitsADownloadOnItsBytesOnly(t *testing.T) {
	for _, tc := range []struct {
		content string
		want    bool
	}{{"remote", true}, {"edited since", false}} {
		w := newOwedWorld(t)
		w.row(statedb.Item{DriveFileID: "F", RelPath: "D/f.txt", Size: 1, ContentMD5: md5hex("x"), LocalMtimeNS: owedMtime.UnixNano()})
		w.file("N/g.txt", tc.content, owedMtime)
		w.op(reconcile.Action{Type: reconcile.Download, RelPath: "N/g.txt", FileID: "F", MD5: md5hex("remote"), Size: 6, Version: 3}, statedb.OpApplied)
		rows := w.recover()
		got := rows["N/g.txt"].DriveFileID == "F" && rows["N/g.txt"].ContentMD5 == md5hex("remote")
		if got != tc.want {
			t.Errorf("content %q: committed=%v, want %v (%v)", tc.content, got, tc.want, rows)
		}
		if tc.want {
			if _, stale := rows["D/f.txt"]; stale {
				t.Errorf("the row's old path survived the commit: %v", rows)
			}
		}
	}
}

// An unpinned download expected its target empty: a row there is not its to drop.
func TestS035RecoveryLeavesADownloadOntoAnotherRowsPath(t *testing.T) {
	w := newOwedWorld(t)
	w.row(statedb.Item{DriveFileID: "G", RelPath: "g.txt", Size: 6, ContentMD5: md5hex("remote"), LocalMtimeNS: owedMtime.UnixNano()})
	w.file("g.txt", "remote", owedMtime)
	w.op(reconcile.Action{Type: reconcile.Download, RelPath: "g.txt", FileID: "F", MD5: md5hex("remote"), Size: 6}, statedb.OpApplied)
	if rows := w.recover(); rows["g.txt"].DriveFileID != "G" {
		t.Fatalf("another row's path was taken: %v", rows)
	}
}

// S035.19 — the executor's path→folder-id map never names a folder Drive no
// longer holds, nor one the plan itself trashes or forgets: an upload into
// such a path fails ("no drive id known") instead of going up into a folder
// in Drive's bin. Was (review of round 1): an edit resurrected under a folder
// deleted on Drive went into that trashed folder, and the next cycle read it
// as deleted — the edit left the tree.
func TestS035UploadNeverTargetsAGoneOrDoomedFolder(t *testing.T) {
	for _, tc := range []struct {
		name string
		live bool                            // the folder is in the snapshot
		plan func(string) []reconcile.Action // extra actions, given the folder id
	}{
		{"gone from Drive", false, func(string) []reconcile.Action { return nil }},
		{"trashed by the plan", true, func(id string) []reconcile.Action {
			return []reconcile.Action{{Type: reconcile.TrashRemote, RelPath: "D", FileID: id, IsDir: true}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			w := newOwedWorld(t)
			fake := driveclient.NewFake()
			root, err := fake.Mkdir(ctx, driveclient.FakeRootID, "Synckeeper")
			if err != nil {
				t.Fatal(err)
			}
			d, err := fake.Mkdir(ctx, root.ID, "D")
			if err != nil {
				t.Fatal(err)
			}
			if !tc.live {
				if err := fake.Trash(ctx, d.ID); err != nil {
					t.Fatal(err)
				}
			}
			w.row(statedb.Item{DriveFileID: d.ID, RelPath: "D", IsDir: true})
			w.file("D/x.txt", "an edit, its only copy", owedMtime)
			live := map[string]bool{root.ID: true}
			if tc.live {
				live[d.ID] = true
			}
			x := &Executor{DB: w.db, Client: fake, SyncDir: w.syncDir, RootID: root.ID,
				QuarantineDir: filepath.Join(filepath.Dir(w.syncDir), "q"), LiveFolders: live}
			plan := append([]reconcile.Action{{Type: reconcile.Upload, RelPath: "D/x.txt"}}, tc.plan(d.ID)...)
			sum, err := x.Apply(ctx, plan)
			if err != nil {
				t.Fatal(err)
			}
			kids, _ := fake.List(ctx, d.ID)
			if len(kids) != 0 {
				t.Fatalf("uploaded into folder %s, which Drive no longer holds or the plan trashes: %+v", d.ID, kids)
			}
			if sum.Failed == 0 || !strings.Contains(strings.Join(sum.Errors, " "), "no drive id known") {
				t.Fatalf("want the upload refused for want of a parent: %v", sum.Errors)
			}
		})
	}
}
