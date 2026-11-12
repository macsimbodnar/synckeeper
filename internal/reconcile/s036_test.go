package reconcile

import "testing"

// S036 — plans refused every cycle (§4.5 overlap) in shapes the first two
// TestPlanFuzz generators did not make; testing.md S036.*.

// S036.1 — Drive renamed folder D to N and its subfolder E to F; this disk made
// only the inner rename (D/F here, D/E gone). The inner move was not seen as
// done: its destination was looked up at Drive's path N/F, where nothing sits
// until the outer rename runs. So D/E's rows were read at D/E — missing here
// — and restored by downloads at N/F, while pass 2 uploaded D/F's files as new
// at the same N/F paths: §4.5 refused the plan every cycle. All 1,014 of the
// wide generator's refusals at the start of S036 were this shape. Now a done
// folder move's destination is looked up where it sits on disk now — under an
// ancestor Drive renamed and this disk has not, still under the old name — and
// the rows under it meet §4.2 there (S002's rule, one level down).
func s036InnerRenameDone(local, remote string) Input {
	return Input{
		Base: map[string]BaseItem{
			"D":         baseDir("FD"),
			"D/E":       baseDir("FE"),
			"D/E/f.txt": baseFile("F", "X", 3),
			"D/E/g.txt": baseFile("G", "MG", 2),
		},
		Local: map[string]LocalItem{
			"D":         locDir(),
			"D/F":       locDir(),
			"D/F/f.txt": locFileM(local, 3, 2000),
			"D/F/g.txt": locFile("MG", 2),
		},
		Remote: map[string]RemoteItem{
			"N":         remDir("FD", 2),
			"N/F":       remDir("FE", 2),
			"N/F/f.txt": remFile("F", remote, 3, 2),
			"N/F/g.txt": remFile("G", "MG", 2, 1),
		},
	}
}

func TestS036InnerFolderRenameDoneHereNoEdit(t *testing.T) {
	runPlan(t, s036InnerRenameDone("X", "X"), []step{
		{t: MoveLocal, rel: "D", newRel: "N"},
		{t: MoveLocal, rel: "N/E", newRel: "N/F"},
		{t: Record, rel: "N/F/f.txt", fileID: "F"}, // mtime refresh only
	})
}

func TestS036InnerFolderRenameDoneHereRemoteEditDownloads(t *testing.T) {
	runPlan(t, s036InnerRenameDone("X", "Y"), []step{
		{t: MoveLocal, rel: "D", newRel: "N"},
		{t: MoveLocal, rel: "N/E", newRel: "N/F"},
		{t: Download, rel: "N/F/f.txt", fileID: "F"},
	})
}

// g.txt still carries its baseline bytes under F: the evidence (§16.7) that F
// is E, so the edit of f.txt is an edit — an update over the same id.
func TestS036InnerFolderRenameDoneHereLocalEditUpdates(t *testing.T) {
	runPlan(t, s036InnerRenameDone("Y", "X"), []step{
		{t: MoveLocal, rel: "D", newRel: "N"},
		{t: MoveLocal, rel: "N/E", newRel: "N/F"},
		{t: UpdateRemote, rel: "N/F/f.txt", fileID: "F"},
	})
}

func TestS036InnerFolderRenameDoneHereDoubleEditConflicts(t *testing.T) {
	runPlan(t, s036InnerRenameDone("Y", "Z"), []step{
		{t: MoveLocal, rel: "D", newRel: "N"},
		{t: MoveLocal, rel: "N/E", newRel: "N/F"},
		{t: ConflictBackup, rel: "N/F/f.txt", newRel: "N/F/f" + conflictSuffix + ".txt"},
		{t: Upload, rel: "N/F/f" + conflictSuffix + ".txt"},
		{t: Download, rel: "N/F/f.txt", fileID: "F"},
	})
}

// S036.2 — a row gone on both sides (c.txt) whose path a Drive move takes
// (e.txt → c.txt): the move's commit needs the path free in the baseline, so
// the row is forgotten in the moves stage (S031) — and it was forgotten again
// in the deletes stage. Harmless (by id), but one row leaves once.
func TestS036RowGoneBothSidesUnderAMoveIsForgottenOnce(t *testing.T) {
	in := Input{
		Base: map[string]BaseItem{
			"c.txt": baseFile("C", "MC", 2),
			"e.txt": baseFile("E", "ME", 3),
		},
		Local:  map[string]LocalItem{"e.txt": locFile("ME", 3)},
		Remote: map[string]RemoteItem{"c.txt": remFile("E", "ME", 3, 2)},
	}
	runPlan(t, in, []step{
		{t: Forget, rel: "c.txt", fileID: "C"},
		{t: MoveLocal, rel: "e.txt", newRel: "c.txt", fileID: "E"},
		{t: Record, rel: "c.txt", fileID: "E"},
	})
}

// S036.3 — the folder merge (S035, §4.3): Drive renamed D to N while a folder
// N sits here and D is still here. The D left behind, emptied by the merge,
// was removed here (no row, expected empty) — decided from the scanned files
// only. A symlink, an unreadable file or folder, or a held row's file still
// under D is not in the scan: the removal then failed ("directory not empty")
// and the next cycle met D as a new folder. Now D is a new folder at once —
// created on Drive, what the scan does not read left alone and reported.
func s036MergeSource() Input {
	return Input{
		Base: map[string]BaseItem{
			"D":       baseDir("FD"),
			"D/f.txt": baseFile("F", "m1", 3),
		},
		Local: map[string]LocalItem{
			"D":       locDir(),
			"D/f.txt": locFile("m1", 3),
			"N":       locDir(),
		},
		Remote: map[string]RemoteItem{
			"N":       remDir("FD", 2),
			"N/f.txt": remFile("F", "m1", 3, 1),
		},
	}
}

var s036MergeSourceKept = []step{
	{t: MkdirRemote, rel: "D"},
	{t: MoveLocal, rel: "D/f.txt", newRel: "N/f.txt", fileID: "F"},
	{t: Record, rel: "N", fileID: "FD"},
}

func TestS036MergeSourceHoldingASymlinkIsNotRemoved(t *testing.T) {
	in := s036MergeSource()
	in.UnscannedLocal = map[string]string{"D/link": "symlink"}
	runPlan(t, in, s036MergeSourceKept)
}

func TestS036MergeSourceHoldingAnUnreadableFileIsNotRemoved(t *testing.T) {
	in := s036MergeSource()
	in.UnscannedLocal = map[string]string{"D/secret.txt": "permission denied"}
	in.UnreadableFiles = map[string]LocalItem{"D/secret.txt": {Size: 5, MtimeNS: 7}}
	runPlan(t, in, s036MergeSourceKept)
}

// The scan lists an unreadable folder (contents unknown), so it already kept D
// — a guard, not red before the fix.
func TestS036MergeSourceHoldingAnUnreadableFolderIsNotRemoved(t *testing.T) {
	in := s036MergeSource()
	in.Local["D/locked"] = locDir()
	in.UnreadableDirs = map[string]bool{"D/locked": true}
	if hasAction(t, in, QuarantineLocal, "D", "") || !hasAction(t, in, MkdirRemote, "D", "") {
		t.Fatal("D, holding an unreadable folder, must be a new folder, not removed")
	}
}
