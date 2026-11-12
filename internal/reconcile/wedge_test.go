package reconcile

import "testing"

// S002: the §4.5 wedge. Each shape below put one rel_path into the plan as an
// upload and a download; ValidateTransferStage refused the plan, the refusal
// changed nothing, and the next cycle planned the same — the machine stopped
// syncing for good. All three are one mistake made in three places: an item
// was looked up on the other side at a path that ignored a planned directory
// move, so its counterpart was missed and each side was planned on its own,
// both landing on the path the move makes them share (§4.2: every row is
// decided at the path the item will occupy after the planned directory moves).
// runPlan's property net refuses an overlapping plan before the assertions.

// Cause A — the local half of a remote folder rename is already on disk. Both
// machines renamed D to N; the other one synced the rename and then an edit of
// N/f.txt. Pass 1 looked for the row's file at D/f.txt, missed it, and planned
// a "restore" download at N/f.txt while pass 2 uploaded N/f.txt as new. Found
// 2026-11-11, no crash needed. The row is now read at N/f.txt and meets §4.2
// there; the folder's MoveLocal replays as a DB commit (R16).
func s002DoubleFolderRename(local, remote string) Input {
	return Input{
		Base: map[string]BaseItem{
			"D":       baseDir("FD"),
			"D/f.txt": baseFile("F", "X", 3),
		},
		Local: map[string]LocalItem{
			"N":       locDir(),
			"N/f.txt": locFileM(local, 3, 2000),
		},
		Remote: map[string]RemoteItem{
			"N":       remDir("FD", 2),
			"N/f.txt": remFile("F", remote, 3, 2),
		},
	}
}

func TestS002DoubleFolderRenameRemoteEditDownloads(t *testing.T) {
	runPlan(t, s002DoubleFolderRename("X", "Y"), []step{
		{t: MoveLocal, rel: "D", newRel: "N"},
		{t: Download, rel: "N/f.txt", fileID: "F"},
	})
}

// (ii) This machine edited, and the folder holds another file whose bytes are
// unchanged: that file pairs the folder (the §16.7 evidence a local folder
// rename needs), so the edit is an edit — an update over the same id.
func TestS002DoubleFolderRenameLocalEditUpdates(t *testing.T) {
	in := s002DoubleFolderRename("Y", "X")
	in.Base["D/g.txt"] = baseFile("G", "MG", 2)
	in.Local["N/g.txt"] = locFile("MG", 2)
	in.Remote["N/g.txt"] = remFile("G", "MG", 2, 1)
	runPlan(t, in, []step{
		{t: MoveLocal, rel: "D", newRel: "N"},
		{t: UpdateRemote, rel: "N/f.txt", fileID: "F"},
	})
}

// (iv) A single-file folder, this machine edited: no file in N carries a
// baseline file's bytes, so nothing shows N is D rather than a new folder of
// that name. The row stops claiming Drive's file (round 3) and the next cycle
// keeps both: the local bytes as a conflict copy beside Drive's, which is
// never overwritten (TestS002UnrelatedPairAfterEachInterruption).
func TestS002DoubleFolderRenameLoneLocalEditConflicts(t *testing.T) {
	in := s002DoubleFolderRename("Y", "X")
	noWriteOver(t, in, "F")
	runPlan(t, in, []step{
		{t: Forget, rel: "D/f.txt", fileID: "F"},
		{t: MoveLocal, rel: "D", newRel: "N"},
	})
}

// noWriteOver fails if the plan updates or trashes any of the Drive items.
func noWriteOver(t *testing.T, in Input, ids ...string) {
	t.Helper()
	in.Machine, in.Now = "test_box", testNow
	plan, _ := Plan(in)
	for _, a := range plan {
		for _, id := range ids {
			if a.FileID == id && (a.Type == UpdateRemote || a.Type == TrashRemote) {
				t.Errorf("plan writes over Drive item %s: %s %q", id, a.Type, a.RelPath)
			}
		}
	}
}

// S1 (round-2 review): D deleted here and an unrelated N/f.txt created, while
// Drive renamed D to N. Nothing ties N to D but the name. Was (round 1):
// update_remote N/f.txt over F — Drive's file overwritten with unrelated
// bytes. Now the row is forgotten before the folder's commit; the next cycle
// keeps both, Drive's at the name and the local file beside it as a conflict
// copy (TestS002UnrelatedPairAfterEachInterruption).
func TestS002UnrelatedFolderAtTheRenamedNameKeepsBoth(t *testing.T) {
	in := Input{
		Base:   map[string]BaseItem{"D": baseDir("FD"), "D/f.txt": baseFile("F", "X", 3)},
		Local:  map[string]LocalItem{"N": locDir(), "N/f.txt": locFileM("Q", 5, 2000)},
		Remote: map[string]RemoteItem{"N": remDir("FD", 2), "N/f.txt": remFile("F", "X", 3, 1)},
	}
	noWriteOver(t, in, "F")
	runPlan(t, in, []step{
		{t: Forget, rel: "D/f.txt", fileID: "F"},
		{t: MoveLocal, rel: "D", newRel: "N"},
	})
}

// S2 (round-2 review): here `rm -r D; mv E N`, on Drive `mv D N`. N/index.txt
// holds E's bytes, not D's. Was (round 1): update_remote N/index.txt over F1
// — D's file overwritten with E's. Now the row is forgotten first; E's bytes
// stay at N/index.txt this cycle and become the conflict copy beside D's file
// next cycle, so E/index.txt's own Drive item can go with the folder E moved
// away here (to Drive's bin; its bytes are still here). D/other.txt is trashed: D's
// deletion here meets Drive's rename — S020's shape (the rename is decided to
// win, not built yet), unchanged by S002.
func TestS002OtherFolderRenamedOntoTheRenamedNameKeepsBoth(t *testing.T) {
	in := Input{
		Base: map[string]BaseItem{
			"D": baseDir("FD"), "D/index.txt": baseFile("F1", "MI", 3), "D/other.txt": baseFile("F2", "MO", 3),
			"E": baseDir("FE"), "E/index.txt": baseFile("F3", "ME", 4),
		},
		Local: map[string]LocalItem{"N": locDir(), "N/index.txt": locFile("ME", 4)},
		Remote: map[string]RemoteItem{
			"N": remDir("FD", 2), "N/index.txt": remFile("F1", "MI", 3, 1), "N/other.txt": remFile("F2", "MO", 3, 1),
			"E": remDir("FE", 1), "E/index.txt": remFile("F3", "ME", 4, 1),
		},
	}
	noWriteOver(t, in, "F1")
	runPlan(t, in, []step{
		{t: Forget, rel: "D/index.txt", fileID: "F1"},
		{t: MoveLocal, rel: "D", newRel: "N"},
		{t: TrashRemote, rel: "E/index.txt", fileID: "F3"},
		{t: TrashRemote, rel: "D/other.txt", fileID: "F2"},
		{t: TrashRemote, rel: "E", fileID: "FE"},
	})
}

// (iii) Both edited a one-file folder: nothing ties N/f.txt to D/f.txt but the
// name — round 3's unrelated pair: the row is forgotten first, and the next
// cycle's both-new row keeps both (TestS002UnrelatedPairAfterEachInterruption).
func TestS002DoubleFolderRenameDoubleEditConflicts(t *testing.T) {
	runPlan(t, s002DoubleFolderRename("Z", "Y"), []step{
		{t: Forget, rel: "D/f.txt", fileID: "F"},
		{t: MoveLocal, rel: "D", newRel: "N"},
	})
}

func TestS002DoubleFolderRenameNoEditRecords(t *testing.T) {
	// Drive's version moved (the other machine's rename), the bytes did not:
	// a record at N, no transfer. Was a no-op MoveRemote of the file onto
	// itself, through the pairing.
	runPlan(t, s002DoubleFolderRename("X", "X"), []step{
		{t: MoveLocal, rel: "D", newRel: "N"},
		{t: Record, rel: "N/f.txt", fileID: "F"},
	})
}

// Cause B — a local folder rename (one MoveRemote reparent) while Drive gained
// a new item inside the folder under its old name, and a new local file of the
// same name sits in the renamed folder (fuzz seeds 1, 28, 45 at the default
// crash probability, 2 and 19 at SYNCKEEPER_FUZZ_CRASH=1 — there the local file
// is a download that landed on disk and crashed before its row, then the
// folder was renamed). Pass 3 places the Drive item at N/f.txt (§4.3), but
// pass 2 looked for it at N/f.txt on Drive, where it is not until the
// reparent runs, and uploaded the local file as new. Now both-new at N/f.txt.
func s002LocalRenameNewOnBoth(local string) Input {
	return Input{
		Base: map[string]BaseItem{
			"D":       baseDir("FD"),
			"D/a.txt": baseFile("A", "MA", 3), // the evidence that pairs D → N
		},
		Local: map[string]LocalItem{
			"N":       locDir(),
			"N/a.txt": locFile("MA", 3),
			"N/f.txt": locFile(local, 4),
		},
		Remote: map[string]RemoteItem{
			"D":       remDir("FD", 1),
			"D/a.txt": remFile("A", "MA", 3, 1),
			"D/f.txt": remFile("G", "MG", 4, 1),
		},
	}
}

func TestS002LocalFolderRenameSameNewFileAdopts(t *testing.T) {
	runPlan(t, s002LocalRenameNewOnBoth("MG"), []step{
		{t: MoveRemote, rel: "D", newRel: "N", fileID: "FD"},
		{t: Record, rel: "N/f.txt", fileID: "G"},
	})
}

func TestS002LocalFolderRenameDifferentNewFileConflicts(t *testing.T) {
	cp := "N/f" + conflictSuffix + ".txt"
	runPlan(t, s002LocalRenameNewOnBoth("ML"), []step{
		{t: MoveRemote, rel: "D", newRel: "N", fileID: "FD"},
		{t: ConflictBackup, rel: "N/f.txt", newRel: cp},
		{t: Upload, rel: cp},
		{t: Download, rel: "N/f.txt", fileID: "G"},
	})
}

// Cause B in pass 1: the baseline file's Drive item was replaced by a new one
// under the same name (deleted, recreated) while the folder was renamed here
// and the local file edited. Pass 1 looked for the replacement at N/x.txt on
// Drive, missed it, and resurrected the edit as an upload at N/x.txt; pass 3
// downloaded the replacement onto the same path. Now §4.2's replaced row.
func TestS002LocalFolderRenameReplacedRemoteConflicts(t *testing.T) {
	cp := "N/x" + conflictSuffix + ".txt"
	runPlan(t, Input{
		Base: map[string]BaseItem{
			"D":       baseDir("FD"),
			"D/a.txt": baseFile("A", "MA", 3),
			"D/x.txt": baseFile("B", "MX", 3),
		},
		Local: map[string]LocalItem{
			"N":       locDir(),
			"N/a.txt": locFile("MA", 3),
			"N/x.txt": locFileM("MY", 4, 2000), // edited here
		},
		Remote: map[string]RemoteItem{
			"D":       remDir("FD", 1),
			"D/a.txt": remFile("A", "MA", 3, 1),
			"D/x.txt": remFile("C", "MZ", 5, 1), // a different item now
		},
	}, []step{
		{t: MoveRemote, rel: "D", newRel: "N", fileID: "FD"},
		{t: ConflictBackup, rel: "N/x.txt", newRel: cp},
		{t: Upload, rel: cp},
		{t: Download, rel: "N/x.txt", fileID: "C"},
	})
}

// Cause C — Drive moved a file into a folder and renamed the folder; the local
// side made the same file move but not (yet) the folder rename (fuzz seed 1).
// Pass 1's coincident-move branch (R25) looked for the local file at N/x.txt,
// where it lands only once the folder's MoveLocal runs, missed it, and planned
// a download there; pass 2 uploaded D/x.txt, which lands at N/x.txt too.
func s002MovedIntoRenamedFolder(local, remote string) Input {
	return Input{
		Base: map[string]BaseItem{
			"D":     baseDir("FD"),
			"x.txt": baseFile("F", "X", 3),
		},
		Local: map[string]LocalItem{
			"D":       locDir(),
			"D/x.txt": locFileM(local, 3, 2000),
		},
		Remote: map[string]RemoteItem{
			"N":       remDir("FD", 2),
			"N/x.txt": remFile("F", remote, 3, 2),
		},
	}
}

func TestS002MovedIntoRenamedFolderRecords(t *testing.T) {
	runPlan(t, s002MovedIntoRenamedFolder("X", "X"), []step{
		{t: MoveLocal, rel: "D", newRel: "N"},
		{t: Record, rel: "N/x.txt", fileID: "F"},
	})
}

func TestS002MovedIntoRenamedFolderRemoteEditDownloads(t *testing.T) {
	runPlan(t, s002MovedIntoRenamedFolder("X", "Y"), []step{
		{t: MoveLocal, rel: "D", newRel: "N"},
		{t: Download, rel: "N/x.txt", fileID: "F"},
	})
}

func TestS002MovedIntoRenamedFolderLocalEditUpdates(t *testing.T) {
	runPlan(t, s002MovedIntoRenamedFolder("Y", "X"), []step{
		{t: MoveLocal, rel: "D", newRel: "N"},
		{t: UpdateRemote, rel: "N/x.txt", fileID: "F"},
	})
}

func TestS002MovedIntoRenamedFolderDoubleEditConflicts(t *testing.T) {
	cp := "N/x" + conflictSuffix + ".txt"
	runPlan(t, s002MovedIntoRenamedFolder("Z", "Y"), []step{
		{t: MoveLocal, rel: "D", newRel: "N"},
		{t: ConflictBackup, rel: "N/x.txt", newRel: cp},
		{t: Upload, rel: cp},
		{t: Download, rel: "N/x.txt", fileID: "F"},
	})
}

// Cause C at the occupant check: Drive moved a file into a folder and renamed
// the folder, while here a new, untracked file of that name was created in the
// folder under its old name. The move's destination check looked at N/x.txt,
// where the stranger lands only once the folder's MoveLocal runs, so it was
// neither preserved nor seen: pass 1 recorded N/x.txt while pass 2 uploaded the
// stranger as N/x.txt. Now the occupant is preserved as a conflict copy before
// the move reclaims the path (§4.5, R7).
func TestS002MoveIntoRenamedFolderPreservesTheOccupant(t *testing.T) {
	cp := "N/x" + conflictSuffix + ".txt"
	runPlan(t, Input{
		Base: map[string]BaseItem{
			"A":       baseDir("FA"),
			"A/y.txt": baseFile("F", "Y", 3),
			"D":       baseDir("FD"),
		},
		Local: map[string]LocalItem{
			"A":       locDir(),
			"A/y.txt": locFile("Y", 3),
			"D":       locDir(),
			"D/x.txt": locFile("Q", 4), // new here, never synced
		},
		Remote: map[string]RemoteItem{
			"A":       remDir("FA", 1),
			"N":       remDir("FD", 2),
			"N/x.txt": remFile("F", "Y", 3, 2),
		},
	}, []step{
		{t: MoveLocal, rel: "D", newRel: "N"},
		{t: ConflictBackup, rel: "N/x.txt", newRel: cp},
		{t: MoveLocal, rel: "A/y.txt", newRel: "N/x.txt"},
		{t: Upload, rel: cp},
		{t: Record, rel: "N/x.txt", fileID: "F"},
	})
}

// Cause D — R25's shape onto a path the baseline holds for another file (fuzz
// seed 124 at SYNCKEEPER_FUZZ_CRASH=1; no crash needed): b.txt was moved over
// a.txt — on Drive (a.txt's own item deleted, b.txt's moved there) and here.
// R25's branch skipped any destination with a baseline row, so a.txt's row
// resurrected the local file as an upload while b.txt's row downloaded its
// Drive item onto the same path. Decided at a.txt against b.txt's row, whose
// Drive item is the one there; a.txt's row, gone from Drive, is dropped by
// that commit (rows are unique per path).
func s002MovedOverDeleted(local string, remote string) Input {
	return Input{
		Base: map[string]BaseItem{
			"a.txt": baseFile("A", "MA", 3),
			"b.txt": baseFile("B", "MB", 3),
		},
		Local:  map[string]LocalItem{"a.txt": locFileM(local, 3, 2000)},
		Remote: map[string]RemoteItem{"a.txt": remFile("B", remote, 3, 2)},
	}
}

func TestS002MovedOverDeletedSameContentRecords(t *testing.T) {
	runPlan(t, s002MovedOverDeleted("MB", "MB"), []step{
		{t: Record, rel: "a.txt", fileID: "B"},
	})
}

func TestS002MovedOverDeletedDriveEditDownloads(t *testing.T) {
	// The local file is b.txt's own bytes: only Drive changed it.
	runPlan(t, s002MovedOverDeleted("MB", "MC"), []step{
		{t: Download, rel: "a.txt", fileID: "B"},
	})
}

func TestS002MovedOverDeletedUntouchedLocalIsReplaced(t *testing.T) {
	// a.txt untouched here, deleted on Drive (§4.2: removed); b.txt deleted
	// here, moved on Drive (§4.2: downloaded). Both rows at once: b.txt's item
	// replaces a.txt in place, pinned to the scanned file.
	runPlan(t, s002MovedOverDeleted("MA", "MB"), []step{
		{t: Download, rel: "a.txt", fileID: "B"},
	})
}

func TestS002MovedOverDeletedOtherContentConflicts(t *testing.T) {
	// Neither file's bytes: an edit of one of them, no telling which — both
	// kept, Drive's at the name.
	cp := "a" + conflictSuffix + ".txt"
	runPlan(t, s002MovedOverDeleted("MX", "MB"), []step{
		{t: ConflictBackup, rel: "a.txt", newRel: cp},
		{t: Upload, rel: cp},
		{t: Download, rel: "a.txt", fileID: "B"},
	})
}

// W1 (round-2 review): Drive renamed D to N and swapped g.txt with N/x.txt;
// here only D was renamed. The occupant N/x.txt is row D/x.txt's file, read
// through the done move — tracked, its bytes on Drive — so the swap's own rule
// applies, as without the folder rename. Was (round 1): N/x.txt read as
// untracked, a conflict backup and an upload duplicating x's bytes on Drive.
func TestS002SwapUnderADoneFolderMoveMakesNoCopy(t *testing.T) {
	runPlan(t, Input{
		Base:   map[string]BaseItem{"D": baseDir("FD"), "D/x.txt": baseFile("X", "MX", 3), "g.txt": baseFile("G", "MG", 4)},
		Local:  map[string]LocalItem{"N": locDir(), "N/x.txt": locFile("MX", 3), "g.txt": locFile("MG", 4)},
		Remote: map[string]RemoteItem{"N": remDir("FD", 2), "N/x.txt": remFile("G", "MG", 4, 2), "g.txt": remFile("X", "MX", 3, 2)},
	}, []step{
		{t: MoveLocal, rel: "D", newRel: "N"},
		{t: MoveLocal, rel: "g.txt", newRel: "N/x.txt"},
		{t: MoveLocal, rel: "N/x.txt", newRel: "g.txt"},
		{t: Record, rel: "N/x.txt", fileID: "G"},
		{t: Record, rel: "g.txt", fileID: "X"},
	})
}

// W2 (round-2 review): Drive renamed D to N, deleted x's item and moved g.txt
// onto N/x.txt; here only D was renamed, x unchanged. §4.2 bins x and moves g:
// the bin runs first, in the moves stage, and the move expects the path empty.
// Was: x quarantined after the move, pinned to x's stat — refused every time
// (a failed action), or g's file binned when the two stats coincided. The same
// held without the folder rename (W2flat).
func TestS002MoveOntoADeletedUnchangedFileBinsItFirst(t *testing.T) {
	for name, in := range map[string]Input{
		"under a done folder move": {
			Base:   map[string]BaseItem{"D": baseDir("FD"), "D/x.txt": baseFile("X", "MX", 3), "g.txt": baseFile("G", "MG", 4)},
			Local:  map[string]LocalItem{"N": locDir(), "N/x.txt": locFile("MX", 3), "g.txt": locFile("MG", 4)},
			Remote: map[string]RemoteItem{"N": remDir("FD", 2), "N/x.txt": remFile("G", "MG", 4, 2)},
		},
		"flat": {
			Base:   map[string]BaseItem{"x.txt": baseFile("X", "MX", 3), "g.txt": baseFile("G", "MG", 4)},
			Local:  map[string]LocalItem{"x.txt": locFile("MX", 3), "g.txt": locFile("MG", 4)},
			Remote: map[string]RemoteItem{"x.txt": remFile("G", "MG", 4, 2)},
		},
	} {
		t.Run(name, func(t *testing.T) {
			at := "x.txt"
			want := []step{}
			if name != "flat" {
				at = "N/x.txt"
				want = append(want, step{t: MoveLocal, rel: "D", newRel: "N"})
			}
			want = append(want,
				step{t: QuarantineLocal, rel: at, fileID: "X"},
				step{t: MoveLocal, rel: "g.txt", newRel: at},
				step{t: Record, rel: at, fileID: "G"})
			runPlan(t, in, want)
			in.Machine, in.Now = "test_box", testNow
			plan, _ := Plan(in)
			for _, a := range plan {
				if a.Type == MoveLocal && a.RelPath == "g.txt" && a.LocalExists {
					t.Errorf("the move pins an occupant the bin is to remove: %+v", a)
				}
			}
		})
	}
}

// W2's edited arm: x was edited here and its Drive item is gone — its bytes
// exist only here, so the move must not clobber them. Kept as a conflict copy
// and uploaded (edit beats delete); x's own resurrecting upload would read g's
// file after the move, so it is not planned. Was: the move clobbered x's edit
// (pinned to its stat), then x's resurrection uploaded g's bytes.
func TestS002MoveOntoADeletedEditedFileKeepsTheEdit(t *testing.T) {
	cp := "x" + conflictSuffix + ".txt"
	runPlan(t, Input{
		Base:   map[string]BaseItem{"x.txt": baseFile("X", "MX", 3), "g.txt": baseFile("G", "MG", 4)},
		Local:  map[string]LocalItem{"x.txt": locFileM("MY", 5, 2000), "g.txt": locFile("MG", 4)},
		Remote: map[string]RemoteItem{"x.txt": remFile("G", "MG", 4, 2)},
	}, []step{
		{t: ConflictBackup, rel: "x.txt", newRel: cp},
		{t: Forget, rel: "x.txt", fileID: "X"},
		{t: MoveLocal, rel: "g.txt", newRel: "x.txt"},
		{t: Upload, rel: cp},
		{t: Record, rel: "x.txt", fileID: "G"},
	})
}

// --- Round 3 ---------------------------------------------------------------

func hasAction(t *testing.T, in Input, typ Type, rel, id string) bool {
	t.Helper()
	in.Machine, in.Now = "test_box", testNow
	plan, _ := Plan(in)
	for _, a := range plan {
		if a.Type == typ && a.RelPath == rel && (id == "" || a.FileID == id) {
			return true
		}
	}
	return false
}

// An empty file is not evidence (round-3 review): every new package has an
// empty __init__.py, every placeholder folder an empty .gitkeep. With D
// deleted here and a new N built beside Drive's renamed D, only those match —
// N/f.txt's unrelated bytes must not go up over F.
func TestS002EmptyFileIsNoEvidence(t *testing.T) {
	for name, in := range map[string]Input{
		"__init__.py": {
			Base: map[string]BaseItem{"D": baseDir("FD"), "D/__init__.py": baseFile("I", "EMPTY", 0),
				"D/f.txt": baseFile("F", "X", 3), "D/g.txt": baseFile("G", "MG", 3)},
			Local: map[string]LocalItem{"N": locDir(), "N/__init__.py": locFile("EMPTY", 0), "N/f.txt": locFileM("Q", 5, 2000)},
			Remote: map[string]RemoteItem{"N": remDir("FD", 2), "N/__init__.py": remFile("I", "EMPTY", 0, 1),
				"N/f.txt": remFile("F", "X", 3, 1), "N/g.txt": remFile("G", "MG", 3, 1)},
		},
		".gitkeep": {
			Base: map[string]BaseItem{"D": baseDir("FD"), "D/sub": baseDir("FS"), "D/sub/.gitkeep": baseFile("K", "EMPTY", 0),
				"D/report.docx": baseFile("F", "X", 3)},
			Local: map[string]LocalItem{"N": locDir(), "N/sub": locDir(), "N/sub/.gitkeep": locFile("EMPTY", 0),
				"N/report.docx": locFileM("Q", 5, 2000)},
			Remote: map[string]RemoteItem{"N": remDir("FD", 2), "N/sub": remDir("FS", 1), "N/sub/.gitkeep": remFile("K", "EMPTY", 0, 1),
				"N/report.docx": remFile("F", "X", 3, 1)},
		},
	} {
		t.Run(name, func(t *testing.T) {
			noWriteOver(t, in, "F")
			rel := "D/f.txt"
			if name == ".gitkeep" {
				rel = "D/report.docx"
			}
			if !hasAction(t, in, Forget, rel, "F") {
				t.Errorf("the row of an unrelated pair is not forgotten: no forget %s", rel)
			}
		})
	}
}

// Both renamed D to N and f.txt to g.txt; this machine then made a new
// N/f.txt (round-3 review). Drive's item is at N/g.txt and the local N/g.txt
// holds its baseline bytes: that is the file — recorded there; N/f.txt is a
// new file — uploaded. Was (round 2): N/f.txt read as the row's file, a
// conflict at N/f.txt plus a download to N/g.txt, and pass 2's upload of
// N/g.txt: §4.5 refused every cycle.
func TestS002BothRenamedFolderAndFileThenNewFileAtTheOldName(t *testing.T) {
	runPlan(t, Input{
		Base:   map[string]BaseItem{"D": baseDir("FD"), "D/f.txt": baseFile("F", "X", 3)},
		Local:  map[string]LocalItem{"N": locDir(), "N/f.txt": locFileM("Q", 5, 2000), "N/g.txt": locFile("X", 3)},
		Remote: map[string]RemoteItem{"N": remDir("FD", 2), "N/g.txt": remFile("F", "X", 3, 2)},
	}, []step{
		{t: MoveLocal, rel: "D", newRel: "N"},
		{t: Upload, rel: "N/f.txt"},
		{t: Record, rel: "N/g.txt", fileID: "F"},
	})
}

// The unrelated pair's next cycles (round-3 review, item 3): once the row is
// forgotten — before the folder's commit, in the same plan — F is a Drive-only
// item and N/f.txt a new local file. Every state an interruption can leave
// meets §4.2's both-new rows; none plans a trash or a quarantine.
func TestS002UnrelatedPairAfterEachInterruption(t *testing.T) {
	cp := "N/f" + conflictSuffix + ".txt"
	for name, c := range map[string]struct {
		in   Input
		want []step
	}{
		"after the forget only": {Input{
			Base:   map[string]BaseItem{"D": baseDir("FD")},
			Local:  map[string]LocalItem{"N": locDir(), "N/f.txt": locFile("Q", 5)},
			Remote: map[string]RemoteItem{"N": remDir("FD", 2), "N/f.txt": remFile("F", "X", 3, 1)},
		}, []step{
			{t: MoveLocal, rel: "D", newRel: "N"},
			{t: ConflictBackup, rel: "N/f.txt", newRel: cp},
			{t: Upload, rel: cp},
			{t: Download, rel: "N/f.txt", fileID: "F"},
		}},
		"after the folder's commit": {Input{
			Base:   map[string]BaseItem{"N": baseDir("FD")},
			Local:  map[string]LocalItem{"N": locDir(), "N/f.txt": locFile("Q", 5)},
			Remote: map[string]RemoteItem{"N": remDir("FD", 2), "N/f.txt": remFile("F", "X", 3, 1)},
		}, []step{
			{t: ConflictBackup, rel: "N/f.txt", newRel: cp},
			{t: Upload, rel: cp},
			{t: Download, rel: "N/f.txt", fileID: "F"},
		}},
		"after the backup, before the download": {Input{
			Base:   map[string]BaseItem{"N": baseDir("FD")},
			Local:  map[string]LocalItem{"N": locDir(), cp: locFile("Q", 5)},
			Remote: map[string]RemoteItem{"N": remDir("FD", 2), "N/f.txt": remFile("F", "X", 3, 1)},
		}, []step{
			{t: Upload, rel: cp},
			{t: Download, rel: "N/f.txt", fileID: "F"},
		}},
		"after the copy's upload": {Input{
			Base:   map[string]BaseItem{"N": baseDir("FD"), cp: baseFile("C", "Q", 5)},
			Local:  map[string]LocalItem{"N": locDir(), cp: locFile("Q", 5)},
			Remote: map[string]RemoteItem{"N": remDir("FD", 2), "N/f.txt": remFile("F", "X", 3, 1), cp: remFile("C", "Q", 5, 1)},
		}, []step{
			{t: Download, rel: "N/f.txt", fileID: "F"},
		}},
	} {
		t.Run(name, func(t *testing.T) { runPlan(t, c.in, c.want) })
	}
}
