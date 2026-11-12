package executor

// S038 — the gate's case-only arm: on a disk that folds case, a rename whose
// destination is another spelling of its source (a folder or a pinned file)
// renames that entry; a folder move replayed as a commit leaves the folder
// spelled as asked; a disk that takes such a rename as a no-op (Linux
// casefold) goes through a temporary name, and recovery finishes one cut
// between its two steps. MkdirLocal never binds Drive's folder to a folder
// stored here under another spelling.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/names"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

func s038Exec(t *testing.T) (*Executor, *owedWorld) {
	t.Helper()
	w := newOwedWorld(t)
	if !names.CaseInsensitiveFS(w.syncDir) {
		t.Skip("sync dir is on a case-sensitive filesystem")
	}
	return &Executor{DB: w.db, Client: driveclient.NewFake(), SyncDir: w.syncDir,
		QuarantineDir: filepath.Join(filepath.Dir(w.syncDir), "quarantine"), RootID: "root"}, w
}

// s038Stored lists the names stored in dir.
func s038Stored(t *testing.T, dir string) map[string]bool {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, e := range ents {
		out[e.Name()] = true
	}
	return out
}

// s038NoOpRename stands for a disk where a rename onto the same entry changes
// nothing (Linux casefold), for the length of the test.
func s038NoOpRename(t *testing.T) {
	t.Helper()
	caseRenameInPlace = func(string, string) error { return nil }
	t.Cleanup(func() { caseRenameInPlace = os.Rename })
}

// S038.20 — move_local D → d of a folder: renamed (was: refused, "a file
// appeared after the scan"), its rows re-homed; in place, and through the
// temporary name where the rename in place changes nothing.
func TestS038CaseOnlyFolderMoveLocal(t *testing.T) {
	for _, noop := range []bool{false, true} {
		x, w := s038Exec(t)
		if noop {
			s038NoOpRename(t)
		}
		w.row(statedb.Item{DriveFileID: "FD", RelPath: "D", IsDir: true})
		w.row(statedb.Item{DriveFileID: "F", RelPath: "D/f.txt", Size: 1, ContentMD5: md5hex("x"), LocalMtimeNS: owedMtime.UnixNano()})
		w.file("D/f.txt", "x", owedMtime)
		sum, err := x.Apply(context.Background(), []reconcile.Action{
			{Type: reconcile.MoveLocal, RelPath: "D", NewRelPath: "d", IsDir: true}})
		if err != nil || sum.Failed != 0 {
			t.Fatalf("noop=%v: %v %v", noop, err, sum.Errors)
		}
		if got := s038Stored(t, w.syncDir); !got["d"] || got["D"] || len(got) != 1 {
			t.Fatalf("noop=%v: stored %v, want only d", noop, got)
		}
		rows, _ := w.db.AllItems()
		for _, it := range rows {
			if it.RelPath != "d" && it.RelPath != "d/f.txt" {
				t.Errorf("noop=%v: row at %s, want under d", noop, it.RelPath)
			}
		}
	}
}

// S038.21 — a pinned case-only file move (S030's arm) gets the same fallback:
// on a disk that takes the rename in place as a no-op the file still ends
// spelled as asked (was: the commit re-homed the row, the disk kept the old
// spelling, the next cycle read it as a rename here and sent it back up).
func TestS038CaseOnlyFileMoveLocalWhereTheRenameIsANoOp(t *testing.T) {
	x, w := s038Exec(t)
	s038NoOpRename(t)
	info := w.file("a.txt", "x", owedMtime)
	w.row(statedb.Item{DriveFileID: "F", RelPath: "a.txt", Size: 1, ContentMD5: md5hex("x"), LocalMtimeNS: owedMtime.UnixNano()})
	sum, err := x.Apply(context.Background(), []reconcile.Action{{Type: reconcile.MoveLocal, RelPath: "a.txt", NewRelPath: "A.txt",
		FileID: "F", LocalExists: true, LocalSize: info.Size(), LocalMtimeNS: info.ModTime().UnixNano()}})
	if err != nil || sum.Failed != 0 {
		t.Fatalf("%v %v", err, sum.Errors)
	}
	if got := s038Stored(t, w.syncDir); !got["A.txt"] || len(got) != 1 {
		t.Fatalf("stored %v, want only A.txt", got)
	}
	// A pin that no longer matches (written meanwhile) refuses, untouched.
	w.file("A.txt", "edited", owedMtime.Add(time.Hour))
	sum, _ = x.Apply(context.Background(), []reconcile.Action{{Type: reconcile.MoveLocal, RelPath: "A.txt", NewRelPath: "a.txt",
		FileID: "F", LocalExists: true, LocalSize: info.Size(), LocalMtimeNS: info.ModTime().UnixNano()}})
	if sum.Failed != 1 || !s038Stored(t, w.syncDir)["A.txt"] {
		t.Fatalf("want the move refused and A.txt kept: %v %v", sum.Errors, s038Stored(t, w.syncDir))
	}
}

// S038.22 — a folder move replayed as a commit (source gone) whose folder the
// disk holds under another spelling of the destination — this machine renamed
// it too, in another case — ends spelled as Drive's name.
func TestS038FolderMoveReplayFixesTheSpelling(t *testing.T) {
	x, w := s038Exec(t)
	w.row(statedb.Item{DriveFileID: "FD", RelPath: "D", IsDir: true})
	w.file("e/f.txt", "x", owedMtime)
	sum, err := x.Apply(context.Background(), []reconcile.Action{
		{Type: reconcile.MoveLocal, RelPath: "D", NewRelPath: "E", IsDir: true}})
	if err != nil || sum.Failed != 0 {
		t.Fatalf("%v %v", err, sum.Errors)
	}
	if got := s038Stored(t, w.syncDir); !got["E"] || len(got) != 1 {
		t.Fatalf("stored %v, want only E", got)
	}
}

// S038.23 — crash recovery (§4.6): a case-only folder rename whose commit was
// lost — journaled applied, or not (its own mark lost): on a folding disk the
// source "D" still stats (it is d), so "gone" is by exact spelling. And one
// cut between the two steps of the temporary-name route: recovery finishes the
// rename, then commits it.
func TestS038RecoveryOfACaseOnlyFolderRename(t *testing.T) {
	for _, state := range []string{statedb.OpApplied, statedb.OpInProgress} {
		_, w := s038Exec(t)
		w.row(statedb.Item{DriveFileID: "FD", RelPath: "D", IsDir: true})
		w.file("d/f.txt", "x", owedMtime)
		w.op(reconcile.Action{Type: reconcile.MoveLocal, RelPath: "D", NewRelPath: "d", IsDir: true}, state)
		if rows := w.recover(); rows["d"].DriveFileID != "FD" {
			t.Errorf("state %s: rows %v, want the folder's row at d", state, rows)
		}
	}
	_, w := s038Exec(t)
	w.row(statedb.Item{DriveFileID: "FD", RelPath: "D", IsDir: true})
	w.file("d"+".synckeeper-case/f.txt", "x", owedMtime)
	w.op(reconcile.Action{Type: reconcile.MoveLocal, RelPath: "D", NewRelPath: "d", IsDir: true}, statedb.OpInProgress)
	if rows := w.recover(); rows["d"].DriveFileID != "FD" {
		t.Errorf("rows %v, want the folder's row at d", rows)
	}
	if got := s038Stored(t, w.syncDir); !got["d"] || len(got) != 1 {
		t.Fatalf("stored %v, want only d", got)
	}
}

// S038.24 — the net under the planner's holds: MkdirLocal of E where the disk
// holds a folder e is refused (was: MkdirAll "succeeded" on e, binding Drive's
// E to it); an existing folder under the exact name is fine.
func TestS038MkdirLocalRefusesAnotherSpelling(t *testing.T) {
	x, w := s038Exec(t)
	w.file("e/mine.txt", "mine", owedMtime)
	sum, _ := x.Apply(context.Background(), []reconcile.Action{{Type: reconcile.MkdirLocal, RelPath: "E", FileID: "FE", IsDir: true}})
	if sum.Failed != 1 {
		t.Fatalf("want mkdir E refused beside e: %+v", sum)
	}
	sum, _ = x.Apply(context.Background(), []reconcile.Action{{Type: reconcile.MkdirLocal, RelPath: "e", FileID: "FE", IsDir: true}})
	if sum.Failed != 0 {
		t.Fatalf("mkdir e over e: %v", sum.Errors)
	}
}

// S038.36 — round 2's edge: two names of one hard-linked file are not one entry
// for the case arm (they are not fold-equal); fold-equal spellings in one
// folder are. Was: sameEntry said "same entry" for any two names of one inode.
func TestS038SameEntryOnlyForFoldEqualSiblings(t *testing.T) {
	_, w := s038Exec(t)
	w.file("a.txt", "x", owedMtime)
	if err := os.Link(filepath.Join(w.syncDir, "a.txt"), filepath.Join(w.syncDir, "b.txt")); err != nil {
		t.Skipf("no hard links here: %v", err)
	}
	abs := func(rel string) string { return filepath.Join(w.syncDir, rel) }
	if sameEntry(abs("a.txt"), abs("b.txt")) {
		t.Error("a.txt and its hard link b.txt taken for one entry")
	}
	if !sameEntry(abs("a.txt"), abs("A.TXT")) {
		t.Error("a.txt and A.TXT not taken for one entry on a folding disk")
	}
}
