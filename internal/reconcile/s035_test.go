package reconcile

import "testing"

// S035 — reconcile-level tests for the causes found minimizing the nine fuzz
// seeds of S035 (testing.md S035.*).

// S035.1 — a folder this machine still has locally, whose Drive item is gone,
// while Drive holds a NEW folder under the same name (another machine deleted
// D, then made D again with a file in it). Pass 3 materializes the new folder
// (MkdirLocal) and downloads its file; pass 4 saw "a create under the old row's
// folder" and resurrected it with MkdirRemote — a second Drive folder named D.
// Drive allows same-name siblings, §5's "first by id wins" then shadows one of
// them, and the machines stop agreeing (fuzz seeds 92, 162; the end of 66, 91,
// 128). No crash needed.
func TestS035FolderRecreatedOnDriveIsNotResurrectedBesideIt(t *testing.T) {
	in := Input{
		Base:  map[string]BaseItem{"D": baseDir("OLD")},
		Local: map[string]LocalItem{"D": locDir()},
		Remote: map[string]RemoteItem{
			"D":       remDir("NEW", 1),
			"D/f.txt": remFile("F", "m1", 3, 1),
		},
	}
	runPlan(t, in, []step{
		{t: MkdirLocal, rel: "D", fileID: "NEW"},
		{t: Download, rel: "D/f.txt", fileID: "F"},
	})
}

// The same with a local file in the old folder that Drive never had: it is
// uploaded into the new folder, still without a second folder on Drive.
func TestS035FolderRecreatedOnDriveTakesTheLocalNewFile(t *testing.T) {
	in := Input{
		Base: map[string]BaseItem{"D": baseDir("OLD")},
		Local: map[string]LocalItem{
			"D":       locDir(),
			"D/g.txt": locFile("m2", 4),
		},
		Remote: map[string]RemoteItem{
			"D":       remDir("NEW", 1),
			"D/f.txt": remFile("F", "m1", 3, 1),
		},
	}
	runPlan(t, in, []step{
		{t: MkdirLocal, rel: "D", fileID: "NEW"},
		{t: Download, rel: "D/f.txt", fileID: "F"},
		{t: Upload, rel: "D/g.txt"},
	})
}

// S035.10 — a remote folder move onto a folder already here, untracked, with
// the source still here: merged, never a folder rename (refused every cycle
// before). The row's file moves into the destination by itself, the folder's
// row is re-homed there by a commit; the folder left at the source, empty, is
// removed here.
func TestS035FolderMoveOntoAFolderHereIsAMerge(t *testing.T) {
	in := Input{
		Base: map[string]BaseItem{
			"D":       baseDir("FD"),
			"D/f.txt": baseFile("F", "m1", 3),
		},
		Local: map[string]LocalItem{
			"D":          locDir(),
			"D/f.txt":    locFile("m1", 3),
			"N":          locDir(),
			"N/mine.txt": locFile("m2", 4),
		},
		Remote: map[string]RemoteItem{
			"N":       remDir("FD", 2),
			"N/f.txt": remFile("F", "m1", 3, 1),
		},
	}
	// Round 2 (review finding 2): D, emptied by the merge, is removed here
	// (no row, expected empty), not created on Drive.
	runPlan(t, in, []step{
		{t: MoveLocal, rel: "D/f.txt", newRel: "N/f.txt", fileID: "F"},
		{t: Record, rel: "N", fileID: "FD"},
		{t: Upload, rel: "N/mine.txt"},
		{t: QuarantineLocal, rel: "D"},
	})
}

// S035.18 — round 2, finding 1 at plan level: Drive deleted D and renamed the
// synced folder E onto its name; D here still holds x.txt. Merged: E's file
// moves into D, D's row becomes E's folder (Record D FE drops the gone
// folder's row), x.txt meets §4.2 at its own path — unchanged: binned;
// edited: uploaded, into the folder now at D. E, emptied, is removed here.
// Was (round 1): move_local E → D, refused every cycle (D occupied).
func TestS035FolderRenamedOntoADeletedOnesNameIsAMerge(t *testing.T) {
	base := map[string]BaseItem{
		"D": baseDir("FD"), "D/x.txt": baseFile("X", "MX", 3),
		"E": baseDir("FE"), "E/y.txt": baseFile("Y", "MY", 4),
	}
	remote := map[string]RemoteItem{"D": remDir("FE", 2), "D/y.txt": remFile("Y", "MY", 4, 1)}
	for _, tc := range []struct {
		name string
		x    LocalItem
		want []step
	}{
		{"unchanged", locFile("MX", 3), []step{
			{t: MoveLocal, rel: "E/y.txt", newRel: "D/y.txt", fileID: "Y"},
			{t: Record, rel: "D", fileID: "FE"},
			{t: QuarantineLocal, rel: "D/x.txt", fileID: "X"},
			{t: QuarantineLocal, rel: "E"},
		}},
		{"edited", locFileM("EDIT", 9, 2000), []step{
			{t: MoveLocal, rel: "E/y.txt", newRel: "D/y.txt", fileID: "Y"},
			{t: Record, rel: "D", fileID: "FE"},
			{t: Upload, rel: "D/x.txt"},
			{t: QuarantineLocal, rel: "E"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local := map[string]LocalItem{"D": locDir(), "D/x.txt": tc.x, "E": locDir(), "E/y.txt": locFile("MY", 4)}
			runPlan(t, Input{Base: base, Local: local, Remote: remote}, tc.want)
		})
	}
}
