package reconcile

import (
	"testing"
	"time"
)

var testNow = time.Date(2026, 7, 8, 10, 30, 0, 0, time.UTC)

// Builders keep the table cases terse.

func baseFile(id, md5 string, size int64) BaseItem {
	return BaseItem{FileID: id, Size: size, MD5: md5, MtimeNS: 1000, DriveMD5: md5, DriveVersion: 1}
}

func baseDir(id string) BaseItem { return BaseItem{FileID: id, IsDir: true, DriveVersion: 1} }

func locFile(md5 string, size int64) LocalItem {
	return LocalItem{Size: size, MtimeNS: 1000, MD5: md5}
}

func locFileM(md5 string, size, mtime int64) LocalItem {
	return LocalItem{Size: size, MtimeNS: mtime, MD5: md5}
}

func locDir() LocalItem { return LocalItem{IsDir: true} }

func remFile(id, md5 string, size, version int64) RemoteItem {
	return RemoteItem{FileID: id, Size: size, MD5: md5, Version: version}
}

func remDir(id string, version int64) RemoteItem {
	return RemoteItem{FileID: id, IsDir: true, Version: version}
}

type step struct {
	t          Type
	rel        string
	newRel     string
	fileID     string
}

func runPlan(t *testing.T, in Input, want []step) {
	t.Helper()
	in.Machine = "test_box"
	in.Now = testNow
	got, _ := Plan(in)
	if len(got) != len(want) {
		t.Fatalf("plan length = %d, want %d\ngot: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Type != w.t || g.RelPath != w.rel {
			t.Errorf("step %d = %s %q, want %s %q", i, g.Type, g.RelPath, w.t, w.rel)
		}
		if w.newRel != "" && g.NewRelPath != w.newRel {
			t.Errorf("step %d newRelPath = %q, want %q", i, g.NewRelPath, w.newRel)
		}
		if w.fileID != "" && g.FileID != w.fileID {
			t.Errorf("step %d fileID = %q, want %q", i, g.FileID, w.fileID)
		}
	}
}

const conflictSuffix = " (conflict test_box 2026-07-08_103000)"

// --- The 13 decision-table rows -------------------------------------

func TestRowNewLocal(t *testing.T) { // absent / new / absent -> upload
	runPlan(t, Input{
		Base:   map[string]BaseItem{},
		Local:  map[string]LocalItem{"a.txt": locFile("m1", 3)},
		Remote: map[string]RemoteItem{},
	}, []step{{t: Upload, rel: "a.txt"}})
}

func TestRowNewRemote(t *testing.T) { // absent / absent / new -> download
	runPlan(t, Input{
		Base:   map[string]BaseItem{},
		Local:  map[string]LocalItem{},
		Remote: map[string]RemoteItem{"a.txt": remFile("f1", "m1", 3, 1)},
	}, []step{{t: Download, rel: "a.txt", fileID: "f1"}})
}

func TestRowBothNewSameMD5(t *testing.T) { // adopt, no transfer
	runPlan(t, Input{
		Base:   map[string]BaseItem{},
		Local:  map[string]LocalItem{"a.txt": locFile("m1", 3)},
		Remote: map[string]RemoteItem{"a.txt": remFile("f1", "m1", 3, 1)},
	}, []step{{t: Record, rel: "a.txt", fileID: "f1"}})
}

func TestRowBothNewDiffMD5(t *testing.T) { // conflict
	runPlan(t, Input{
		Base:   map[string]BaseItem{},
		Local:  map[string]LocalItem{"a.txt": locFile("mLocal", 3)},
		Remote: map[string]RemoteItem{"a.txt": remFile("f1", "mRemote", 4, 1)},
	}, []step{
		{t: ConflictBackup, rel: "a.txt", newRel: "a" + conflictSuffix + ".txt"},
		{t: Upload, rel: "a" + conflictSuffix + ".txt"},
		{t: Download, rel: "a.txt", fileID: "f1"},
	})
}

func TestRowUnchanged(t *testing.T) { // present / unchanged / unchanged -> nothing
	runPlan(t, Input{
		Base:   map[string]BaseItem{"a.txt": baseFile("f1", "m1", 3)},
		Local:  map[string]LocalItem{"a.txt": locFile("m1", 3)},
		Remote: map[string]RemoteItem{"a.txt": remFile("f1", "m1", 3, 1)},
	}, nil)
}

func TestRowLocalEdit(t *testing.T) { // upload new revision
	runPlan(t, Input{
		Base:   map[string]BaseItem{"a.txt": baseFile("f1", "m1", 3)},
		Local:  map[string]LocalItem{"a.txt": locFileM("m2", 5, 2000)},
		Remote: map[string]RemoteItem{"a.txt": remFile("f1", "m1", 3, 1)},
	}, []step{{t: UpdateRemote, rel: "a.txt", fileID: "f1"}})
}

func TestRowRemoteEdit(t *testing.T) { // download replace
	runPlan(t, Input{
		Base:   map[string]BaseItem{"a.txt": baseFile("f1", "m1", 3)},
		Local:  map[string]LocalItem{"a.txt": locFile("m1", 3)},
		Remote: map[string]RemoteItem{"a.txt": remFile("f1", "m2", 5, 2)},
	}, []step{{t: Download, rel: "a.txt", fileID: "f1"}})
}

func TestRowBothChangedSameMD5(t *testing.T) { // record, no transfer
	runPlan(t, Input{
		Base:   map[string]BaseItem{"a.txt": baseFile("f1", "m1", 3)},
		Local:  map[string]LocalItem{"a.txt": locFileM("m2", 5, 2000)},
		Remote: map[string]RemoteItem{"a.txt": remFile("f1", "m2", 5, 2)},
	}, []step{{t: Record, rel: "a.txt", fileID: "f1"}})
}

func TestRowBothChangedDiffMD5(t *testing.T) { // conflict, remote wins name
	runPlan(t, Input{
		Base:   map[string]BaseItem{"a.txt": baseFile("f1", "m1", 3)},
		Local:  map[string]LocalItem{"a.txt": locFileM("mLocal", 5, 2000)},
		Remote: map[string]RemoteItem{"a.txt": remFile("f1", "mRemote", 6, 2)},
	}, []step{
		{t: ConflictBackup, rel: "a.txt", newRel: "a" + conflictSuffix + ".txt"},
		{t: Upload, rel: "a" + conflictSuffix + ".txt"},
		{t: Download, rel: "a.txt", fileID: "f1"},
	})
}

func TestRowLocalDelete(t *testing.T) { // trash remote
	runPlan(t, Input{
		Base:   map[string]BaseItem{"a.txt": baseFile("f1", "m1", 3)},
		Local:  map[string]LocalItem{},
		Remote: map[string]RemoteItem{"a.txt": remFile("f1", "m1", 3, 1)},
	}, []step{{t: TrashRemote, rel: "a.txt", fileID: "f1"}})
}

func TestRowRemoteDelete(t *testing.T) { // quarantine local
	runPlan(t, Input{
		Base:   map[string]BaseItem{"a.txt": baseFile("f1", "m1", 3)},
		Local:  map[string]LocalItem{"a.txt": locFile("m1", 3)},
		Remote: map[string]RemoteItem{},
	}, []step{{t: QuarantineLocal, rel: "a.txt", fileID: "f1"}})
}

func TestRowEditBeatsDeleteLocalEdit(t *testing.T) { // changed local, trashed remote -> resurrect
	runPlan(t, Input{
		Base:   map[string]BaseItem{"a.txt": baseFile("f1", "m1", 3)},
		Local:  map[string]LocalItem{"a.txt": locFileM("m2", 5, 2000)},
		Remote: map[string]RemoteItem{},
	}, []step{{t: Upload, rel: "a.txt"}})
}

func TestRowEditBeatsDeleteRemoteEdit(t *testing.T) { // deleted local, changed remote -> download
	runPlan(t, Input{
		Base:   map[string]BaseItem{"a.txt": baseFile("f1", "m1", 3)},
		Local:  map[string]LocalItem{},
		Remote: map[string]RemoteItem{"a.txt": remFile("f1", "m2", 5, 2)},
	}, []step{{t: Download, rel: "a.txt", fileID: "f1"}})
}

// --- Beyond the table -------------------------------------------------

func TestBothDeletedForgets(t *testing.T) {
	runPlan(t, Input{
		Base:   map[string]BaseItem{"a.txt": baseFile("f1", "m1", 3)},
		Local:  map[string]LocalItem{},
		Remote: map[string]RemoteItem{},
	}, []step{{t: Forget, rel: "a.txt", fileID: "f1"}})
}

func TestMtimeDriftRecordsWithoutTransfer(t *testing.T) {
	// Touched file, same content: refresh the row so we don't rehash forever.
	runPlan(t, Input{
		Base:   map[string]BaseItem{"a.txt": baseFile("f1", "m1", 3)},
		Local:  map[string]LocalItem{"a.txt": locFileM("m1", 3, 9999)},
		Remote: map[string]RemoteItem{"a.txt": remFile("f1", "m1", 3, 1)},
	}, []step{{t: Record, rel: "a.txt", fileID: "f1"}})
}

func TestLocalMoveIsPaired(t *testing.T) {
	runPlan(t, Input{
		Base:   map[string]BaseItem{"old.txt": baseFile("f1", "m1", 3)},
		Local:  map[string]LocalItem{"new.txt": locFile("m1", 3)},
		Remote: map[string]RemoteItem{"old.txt": remFile("f1", "m1", 3, 1)},
	}, []step{{t: MoveRemote, rel: "old.txt", newRel: "new.txt", fileID: "f1"}})
}

func TestLocalMoveNotPairedWhenContentDiffers(t *testing.T) {
	runPlan(t, Input{
		Base:   map[string]BaseItem{"old.txt": baseFile("f1", "m1", 3)},
		Local:  map[string]LocalItem{"new.txt": locFile("mDiff", 4)},
		Remote: map[string]RemoteItem{"old.txt": remFile("f1", "m1", 3, 1)},
	}, []step{
		{t: Upload, rel: "new.txt"},
		{t: TrashRemote, rel: "old.txt", fileID: "f1"},
	})
}

func TestRemoteMoveBecomesLocalMove(t *testing.T) {
	runPlan(t, Input{
		Base:   map[string]BaseItem{"old.txt": baseFile("f1", "m1", 3)},
		Local:  map[string]LocalItem{"old.txt": locFile("m1", 3)},
		Remote: map[string]RemoteItem{"new.txt": remFile("f1", "m1", 3, 2)},
	}, []step{
		{t: MoveLocal, rel: "old.txt", newRel: "new.txt", fileID: "f1"},
		// The rename bumped the Drive version; the row refresh rides along.
		{t: Record, rel: "new.txt", fileID: "f1"},
	})
}

func TestRemoteDirMoveDoesNotFanOut(t *testing.T) {
	// Dir renamed remotely; children ride along with one local dir move.
	runPlan(t, Input{
		Base: map[string]BaseItem{
			"docs":       baseDir("d1"),
			"docs/a.txt": baseFile("f1", "m1", 3),
		},
		Local: map[string]LocalItem{
			"docs":       locDir(),
			"docs/a.txt": locFile("m1", 3),
		},
		Remote: map[string]RemoteItem{
			"papers":       remDir("d1", 2),
			"papers/a.txt": remFile("f1", "m1", 3, 1),
		},
	}, []step{{t: MoveLocal, rel: "docs", newRel: "papers"}})
}

func TestRemoteMoveOutOfTreeQuarantinesLocal(t *testing.T) {
	// Out-of-tree move surfaces as a remote delete.
	runPlan(t, Input{
		Base:   map[string]BaseItem{"a.txt": baseFile("f1", "m1", 3)},
		Local:  map[string]LocalItem{"a.txt": locFile("m1", 3)},
		Remote: map[string]RemoteItem{},
	}, []step{{t: QuarantineLocal, rel: "a.txt", fileID: "f1"}})
}

func TestNewDirsBothSidesAdopted(t *testing.T) {
	runPlan(t, Input{
		Base:   map[string]BaseItem{},
		Local:  map[string]LocalItem{"docs": locDir()},
		Remote: map[string]RemoteItem{"docs": remDir("d1", 1)},
	}, []step{{t: Record, rel: "docs", fileID: "d1"}})
}

func TestNestedNewLocalTree(t *testing.T) {
	runPlan(t, Input{
		Base: map[string]BaseItem{},
		Local: map[string]LocalItem{
			"a":         locDir(),
			"a/b":       locDir(),
			"a/b/f.txt": locFile("m1", 3),
		},
		Remote: map[string]RemoteItem{},
	}, []step{
		{t: MkdirRemote, rel: "a"},
		{t: MkdirRemote, rel: "a/b"},
		{t: Upload, rel: "a/b/f.txt"},
	})
}

func TestNestedNewRemoteTree(t *testing.T) {
	runPlan(t, Input{
		Base:  map[string]BaseItem{},
		Local: map[string]LocalItem{},
		Remote: map[string]RemoteItem{
			"a":         remDir("d1", 1),
			"a/b":       remDir("d2", 1),
			"a/b/f.txt": remFile("f1", "m1", 3, 1),
		},
	}, []step{
		{t: MkdirLocal, rel: "a", fileID: "d1"},
		{t: MkdirLocal, rel: "a/b", fileID: "d2"},
		{t: Download, rel: "a/b/f.txt", fileID: "f1"},
	})
}

func TestLocalDeletedTreeTrashedBottomUp(t *testing.T) {
	runPlan(t, Input{
		Base: map[string]BaseItem{
			"a":         baseDir("d1"),
			"a/b":       baseDir("d2"),
			"a/b/f.txt": baseFile("f1", "m1", 3),
		},
		Local: map[string]LocalItem{},
		Remote: map[string]RemoteItem{
			"a":         remDir("d1", 1),
			"a/b":       remDir("d2", 1),
			"a/b/f.txt": remFile("f1", "m1", 3, 1),
		},
	}, []step{
		{t: TrashRemote, rel: "a/b/f.txt", fileID: "f1"},
		{t: TrashRemote, rel: "a/b", fileID: "d2"},
		{t: TrashRemote, rel: "a", fileID: "d1"},
	})
}

func TestDeleteFolderVsAddFileResurrects(t *testing.T) {
	// Remote deleted the folder, but local has a NEW file inside: the
	// container is resurrected and the new file uploaded; the unchanged
	// baseline child is quarantined (delete wins for it).
	runPlan(t, Input{
		Base: map[string]BaseItem{
			"a":       baseDir("d1"),
			"a/f.txt": baseFile("f1", "m1", 3),
		},
		Local: map[string]LocalItem{
			"a":         locDir(),
			"a/f.txt":   locFile("m1", 3),
			"a/new.txt": locFile("m2", 5),
		},
		Remote: map[string]RemoteItem{},
	}, []step{
		{t: MkdirRemote, rel: "a"},
		{t: Upload, rel: "a/new.txt"},
		{t: QuarantineLocal, rel: "a/f.txt", fileID: "f1"},
	})
}

func TestRemoteDeletedTreeQuarantinedBottomUp(t *testing.T) {
	runPlan(t, Input{
		Base: map[string]BaseItem{
			"a":       baseDir("d1"),
			"a/f.txt": baseFile("f1", "m1", 3),
		},
		Local: map[string]LocalItem{
			"a":       locDir(),
			"a/f.txt": locFile("m1", 3),
		},
		Remote: map[string]RemoteItem{},
	}, []step{
		{t: QuarantineLocal, rel: "a/f.txt", fileID: "f1"},
		{t: QuarantineLocal, rel: "a", fileID: "d1"},
	})
}

func TestRemoteReplacedInPlace(t *testing.T) {
	// Remote file deleted and a different file (new id) created at the
	// same path; local unchanged: replace in place, no quarantine.
	runPlan(t, Input{
		Base:   map[string]BaseItem{"a.txt": baseFile("f1", "m1", 3)},
		Local:  map[string]LocalItem{"a.txt": locFile("m1", 3)},
		Remote: map[string]RemoteItem{"a.txt": remFile("f2", "m2", 5, 1)},
	}, []step{{t: Download, rel: "a.txt", fileID: "f2"}})
}

func TestTypeClashSkipsAndReports(t *testing.T) {
	_, skips := Plan(Input{
		Base:    map[string]BaseItem{"a.txt": baseFile("f1", "m1", 3)},
		Local:   map[string]LocalItem{"a.txt": locDir()},
		Remote:  map[string]RemoteItem{"a.txt": remFile("f1", "m1", 3, 1)},
		Machine: "test_box",
		Now:     testNow,
	})
	if len(skips) != 1 || skips[0].RelPath != "a.txt" {
		t.Fatalf("skips = %+v, want one for a.txt", skips)
	}
}
