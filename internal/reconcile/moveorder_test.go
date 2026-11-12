package reconcile

// S031: the moves stage could destroy an unsynced local edit. File moves ran
// in depth-then-path order, not by dependency, and a tracked occupant whose
// Drive item was alive was taken for "safe on Drive" even when edited here —
// the incoming move was pinned to the edit's stat and renamed over it.
// §4.5 rule O1 (ValidateLocalOverwrites) is the net; these are the shapes.

import (
	"strings"
	"testing"
)

// --- The net -------------------------------------------------------------

// The plan the planner made for F4 before S031: the move onto f.txt is pinned
// to the edit's stat and runs first. Rule O1 refuses it.
func TestS031RuleO1RefusesAMoveOverAnUnsyncedEdit(t *testing.T) {
	in := s031F4Flat()
	plan := []Action{
		{Type: MoveLocal, RelPath: "a.txt", NewRelPath: "f.txt", FileID: "G", LocalExists: true, LocalSize: 5, LocalMtimeNS: 2000},
		{Type: MoveLocal, RelPath: "f.txt", NewRelPath: "h.txt", FileID: "F"},
	}
	err := ValidateLocalOverwrites(in, plan)
	if _, ok := err.(*UnkeptOverwriteError); !ok {
		t.Fatalf("ValidateLocalOverwrites = %v, want an *UnkeptOverwriteError", err)
	}
	var refused RefusedPlan = err.(*UnkeptOverwriteError)
	if p := refused.RefusedPaths(); len(p) != 2 || p[0] != "f.txt" || p[1] != "f.txt" {
		t.Errorf("RefusedPaths = %v, want [f.txt f.txt]", p)
	}
}

func TestS031RuleO1(t *testing.T) {
	base := map[string]BaseItem{"x.txt": baseFile("X", "MX", 3), "y.txt": baseFile("Y", "MY", 4)}
	edited := locFileM("EDIT", 9, 2000)
	cases := []struct {
		name   string
		local  map[string]LocalItem
		remote map[string]RemoteItem
		plan   []Action
		refuse bool
	}{
		{"pinned move over a synced file",
			map[string]LocalItem{"x.txt": locFile("MX", 3), "y.txt": locFile("MY", 4)}, nil,
			[]Action{{Type: MoveLocal, RelPath: "y.txt", NewRelPath: "x.txt", LocalExists: true, LocalSize: 3, LocalMtimeNS: 1000}},
			false},
		{"pinned download over an edit",
			map[string]LocalItem{"x.txt": edited}, nil,
			[]Action{{Type: Download, RelPath: "x.txt", FileID: "X", LocalExists: true, LocalSize: 9, LocalMtimeNS: 2000}},
			true},
		{"pinned download over an unchanged file whose Drive item moved on",
			map[string]LocalItem{"x.txt": locFile("MX", 3)}, map[string]RemoteItem{"x.txt": remFile("X", "MX2", 5, 2)},
			[]Action{{Type: Download, RelPath: "x.txt", FileID: "X", LocalExists: true, LocalSize: 3, LocalMtimeNS: 1000}},
			false},
		{"pinned download over local bytes Drive holds in another item",
			map[string]LocalItem{"n.txt": locFileM("MR", 7, 3000)}, map[string]RemoteItem{"r.txt": remFile("R", "MR", 7, 1)},
			[]Action{{Type: Download, RelPath: "n.txt", FileID: "N", LocalExists: true, LocalSize: 7, LocalMtimeNS: 3000}},
			false},
		{"unpinned move onto an edit: the gate refuses it",
			map[string]LocalItem{"x.txt": edited, "y.txt": locFile("MY", 4)}, nil,
			[]Action{{Type: MoveLocal, RelPath: "y.txt", NewRelPath: "x.txt"}},
			false},
		{"pin names another file than the edit: the gate refuses it",
			map[string]LocalItem{"x.txt": edited, "y.txt": locFile("MY", 4)}, nil,
			[]Action{{Type: MoveLocal, RelPath: "y.txt", NewRelPath: "x.txt", LocalExists: true, LocalSize: 3, LocalMtimeNS: 1000}},
			false},
		{"the edit leaves first, but its move may fail",
			map[string]LocalItem{"x.txt": edited, "y.txt": locFile("MY", 4)}, nil,
			[]Action{
				{Type: MoveLocal, RelPath: "x.txt", NewRelPath: "z.txt"},
				{Type: MoveLocal, RelPath: "y.txt", NewRelPath: "x.txt", LocalExists: true, LocalSize: 9, LocalMtimeNS: 2000},
			},
			true},
		{"the edit leaves first and the move is ProtectedBy it",
			map[string]LocalItem{"x.txt": edited, "y.txt": locFile("MY", 4)}, nil,
			[]Action{
				{Type: MoveLocal, RelPath: "x.txt", NewRelPath: "z.txt"},
				{Type: MoveLocal, RelPath: "y.txt", NewRelPath: "x.txt", ProtectedBy: "x.txt", LocalExists: true, LocalSize: 9, LocalMtimeNS: 2000},
			},
			false},
		{"the edit is backed up and the download is ProtectedBy the backup",
			map[string]LocalItem{"x.txt": edited}, nil,
			[]Action{
				{Type: ConflictBackup, RelPath: "x.txt", NewRelPath: "x (c).txt"},
				{Type: Download, RelPath: "x.txt", FileID: "X", ProtectedBy: "x.txt", LocalExists: true, LocalSize: 9, LocalMtimeNS: 2000},
			},
			false},
		{"protection is transitive",
			map[string]LocalItem{"x.txt": edited, "y.txt": locFile("MY", 4)}, nil,
			[]Action{
				{Type: ConflictBackup, RelPath: "x.txt", NewRelPath: "x (c).txt"},
				{Type: Forget, RelPath: "q.txt", ProtectedBy: "x.txt"},
				{Type: MoveLocal, RelPath: "y.txt", NewRelPath: "x.txt", ProtectedBy: "q.txt", LocalExists: true, LocalSize: 9, LocalMtimeNS: 2000},
			},
			false},
		{"a file moved in by an earlier move is still looked at",
			map[string]LocalItem{"w.txt": edited}, nil,
			[]Action{
				{Type: MoveLocal, RelPath: "w.txt", NewRelPath: "x.txt"},
				{Type: Download, RelPath: "x.txt", FileID: "X", LocalExists: true, LocalSize: 9, LocalMtimeNS: 2000},
			},
			true},
		{"bin an edit",
			map[string]LocalItem{"x.txt": edited}, nil,
			[]Action{{Type: QuarantineLocal, RelPath: "x.txt", FileID: "X", LocalExists: true, LocalSize: 9, LocalMtimeNS: 2000}},
			true},
		{"bin an unchanged file",
			map[string]LocalItem{"x.txt": locFile("MX", 3)}, nil,
			[]Action{{Type: QuarantineLocal, RelPath: "x.txt", FileID: "X", LocalExists: true, LocalSize: 3, LocalMtimeNS: 1000}},
			false},
		{"a collapsed folder delete binning an edit",
			map[string]LocalItem{"d": locDir(), "d/x.txt": edited}, nil,
			[]Action{{Type: QuarantineLocal, RelPath: "d", IsDir: true, Subtree: []SubtreeEntry{
				{RelPath: "d/x.txt", FileID: "X", Size: 9, MtimeNS: 2000}}}},
			true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateLocalOverwrites(Input{Base: base, Local: c.local, Remote: c.remote}, c.plan)
			if (err != nil) != c.refuse {
				t.Errorf("ValidateLocalOverwrites = %v, want refused=%v", err, c.refuse)
			}
		})
	}
}

// --- The shapes ----------------------------------------------------------

// F4 flat: another machine renamed f.txt → h.txt and moved a.txt onto the
// name f.txt; here f.txt was edited and not yet synced. Only this machine
// changed f's content: the edit follows the file to h.txt and goes up into
// F (§4.2); a's bytes land at f.txt. Was: a.txt renamed over the edit (no
// copy), then f.txt → h.txt moved a's bytes and uploaded them into F.
func s031F4Flat() Input {
	return Input{
		Base:   map[string]BaseItem{"f.txt": baseFile("F", "X", 3), "a.txt": baseFile("G", "MG", 4)},
		Local:  map[string]LocalItem{"f.txt": locFileM("Q", 5, 2000), "a.txt": locFile("MG", 4)},
		Remote: map[string]RemoteItem{"h.txt": remFile("F", "X", 3, 2), "f.txt": remFile("G", "MG", 4, 2)},
	}
}

func TestS031F4FlatEditFollowsItsFile(t *testing.T) {
	runPlan(t, s031F4Flat(), []step{
		{t: MoveLocal, rel: "f.txt", newRel: "h.txt"},
		{t: MoveLocal, rel: "a.txt", newRel: "f.txt"},
		{t: Record, rel: "f.txt", fileID: "G"},
		{t: UpdateRemote, rel: "h.txt", fileID: "F"},
	})
	noPinnedMoves(t, s031F4Flat())
}

// F4 one folder deeper: g.txt sorts before S/f.txt by depth whatever the
// names, so depth order ran the clobbering move first.
func TestS031F4DeeperEditFollowsItsFile(t *testing.T) {
	in := Input{
		Base:  map[string]BaseItem{"S": baseDir("FS"), "S/f.txt": baseFile("F", "X", 3), "g.txt": baseFile("G", "MG", 4)},
		Local: map[string]LocalItem{"S": locDir(), "S/f.txt": locFileM("Q", 5, 2000), "g.txt": locFile("MG", 4)},
		Remote: map[string]RemoteItem{"S": remDir("FS", 1), "S/h.txt": remFile("F", "X", 3, 2),
			"S/f.txt": remFile("G", "MG", 4, 2)},
	}
	runPlan(t, in, []step{
		{t: MoveLocal, rel: "S/f.txt", newRel: "S/h.txt"},
		{t: MoveLocal, rel: "g.txt", newRel: "S/f.txt"},
		{t: Record, rel: "S/f.txt", fileID: "G"},
		{t: UpdateRemote, rel: "S/h.txt", fileID: "F"},
	})
	noPinnedMoves(t, in)
}

// A chain of three, its middle file edited here: c→d must run before b→c,
// and b→c before a→b.
func TestS031ChainOfThreeRunsInDependencyOrder(t *testing.T) {
	in := Input{
		Base: map[string]BaseItem{"a.txt": baseFile("A", "MA", 3), "b.txt": baseFile("B", "MB", 4),
			"c.txt": baseFile("C", "MC", 5)},
		Local: map[string]LocalItem{"a.txt": locFile("MA", 3), "b.txt": locFileM("EDIT", 6, 2000),
			"c.txt": locFile("MC", 5)},
		Remote: map[string]RemoteItem{"b.txt": remFile("A", "MA", 3, 2), "c.txt": remFile("B", "MB", 4, 2),
			"d.txt": remFile("C", "MC", 5, 2)},
	}
	runPlan(t, in, []step{
		{t: MoveLocal, rel: "c.txt", newRel: "d.txt"},
		{t: MoveLocal, rel: "b.txt", newRel: "c.txt"},
		{t: MoveLocal, rel: "a.txt", newRel: "b.txt"},
		{t: Record, rel: "b.txt", fileID: "A"},
		{t: UpdateRemote, rel: "c.txt", fileID: "B"},
		{t: Record, rel: "d.txt", fileID: "C"},
	})
	noPinnedMoves(t, in)
}

// A swap made by id on Drive: a cycle. Broken through a temporary name in the
// same folder, committed like any move — no move lands on an occupied path,
// and no commit meets another row at its destination (was: both commits
// failed `UNIQUE constraint failed: items.rel_path` for a cycle or two).
func s031Swap(aLocal LocalItem) Input {
	return Input{
		Base:   map[string]BaseItem{"a.txt": baseFile("A", "MA", 3), "b.txt": baseFile("B", "MB", 4)},
		Local:  map[string]LocalItem{"a.txt": aLocal, "b.txt": locFile("MB", 4)},
		Remote: map[string]RemoteItem{"a.txt": remFile("B", "MB", 4, 2), "b.txt": remFile("A", "MA", 3, 2)},
	}
}

func TestS031SwapByIDGoesThroughATemporaryName(t *testing.T) {
	tmp := "a" + conflictSuffix + ".txt"
	runPlan(t, s031Swap(locFile("MA", 3)), []step{
		{t: MoveLocal, rel: "a.txt", newRel: tmp},
		{t: MoveLocal, rel: "b.txt", newRel: "a.txt"},
		{t: MoveLocal, rel: tmp, newRel: "b.txt"},
		{t: Record, rel: "a.txt", fileID: "B"},
		{t: Record, rel: "b.txt", fileID: "A"},
	})
	noPinnedMoves(t, s031Swap(locFile("MA", 3)))
	plan := planOf(s031Swap(locFile("MA", 3)))
	// Each move into a path another move vacates depends on that move: a
	// vacate that fails — even after its rename, at its commit — must stop the
	// move, or the next cycle reads the wrong bytes at the name as an edit.
	if plan[1].ProtectedBy != "a.txt" || plan[2].ProtectedBy != "b.txt" {
		t.Errorf("protectors: b.txt → a.txt %q, temporary → b.txt %q; want a.txt, b.txt", plan[1].ProtectedBy, plan[2].ProtectedBy)
	}
	if plan[4].ProtectedBy != tmp {
		t.Errorf("b.txt's record is ProtectedBy %q, want %q (the move that puts the file there)", plan[4].ProtectedBy, tmp)
	}
}

func TestS031SwapWithOneSideEditedKeepsTheEdit(t *testing.T) {
	tmp := "a" + conflictSuffix + ".txt"
	in := s031Swap(locFileM("EDIT", 9, 2000))
	runPlan(t, in, []step{
		{t: MoveLocal, rel: "a.txt", newRel: tmp},
		{t: MoveLocal, rel: "b.txt", newRel: "a.txt"},
		{t: MoveLocal, rel: tmp, newRel: "b.txt"},
		{t: Record, rel: "a.txt", fileID: "B"},
		{t: UpdateRemote, rel: "b.txt", fileID: "A"},
	})
	noPinnedMoves(t, in)
}

// A three-way rotation by id: a→b→c→a.
func TestS031RotationOfThree(t *testing.T) {
	tmp := "a" + conflictSuffix + ".txt"
	in := Input{
		Base: map[string]BaseItem{"a.txt": baseFile("A", "MA", 3), "b.txt": baseFile("B", "MB", 4),
			"c.txt": baseFile("C", "MC", 5)},
		Local: map[string]LocalItem{"a.txt": locFile("MA", 3), "b.txt": locFile("MB", 4), "c.txt": locFile("MC", 5)},
		Remote: map[string]RemoteItem{"b.txt": remFile("A", "MA", 3, 2), "c.txt": remFile("B", "MB", 4, 2),
			"a.txt": remFile("C", "MC", 5, 2)},
	}
	runPlan(t, in, []step{
		{t: MoveLocal, rel: "a.txt", newRel: tmp},
		{t: MoveLocal, rel: "c.txt", newRel: "a.txt"},
		{t: MoveLocal, rel: "b.txt", newRel: "c.txt"},
		{t: MoveLocal, rel: tmp, newRel: "b.txt"},
		{t: Record, rel: "a.txt", fileID: "C"},
		{t: Record, rel: "b.txt", fileID: "A"},
		{t: Record, rel: "c.txt", fileID: "B"},
	})
}

// F1: under a folder move the disk already shows, the forget arm (strangers
// sharing a name, S002) emitted no move for its row, so g.txt's move onto
// N/f.txt took the edit there for the forgotten row's file, "safe on Drive",
// and clobbered it. The forgotten row claims nothing: the occupant is kept
// like an untracked one — a conflict copy, uploaded — before the move.
func s031F1() Input {
	return Input{
		Base:  map[string]BaseItem{"D": baseDir("FD"), "D/f.txt": baseFile("F", "X", 3), "g.txt": baseFile("G", "MG", 4)},
		Local: map[string]LocalItem{"N": locDir(), "N/f.txt": locFileM("Q", 5, 2000), "g.txt": locFile("MG", 4)},
		Remote: map[string]RemoteItem{"N": remDir("FD", 2), "N/h.txt": remFile("F", "X", 3, 2),
			"N/f.txt": remFile("G", "MG", 4, 2)},
	}
}

func TestS031F1ForgottenRowsFileIsKept(t *testing.T) {
	cp := "N/f" + conflictSuffix + ".txt"
	runPlan(t, s031F1(), []step{
		{t: Forget, rel: "D/f.txt", fileID: "F"},
		{t: MoveLocal, rel: "D", newRel: "N"},
		{t: ConflictBackup, rel: "N/f.txt", newRel: cp},
		{t: MoveLocal, rel: "g.txt", newRel: "N/f.txt"},
		{t: Upload, rel: cp},
		{t: Record, rel: "N/f.txt", fileID: "G"},
	})
	noWriteOver(t, s031F1(), "F")
}

// F2: the same plus N/h.txt here with F's baseline bytes — that is the row's
// file (both renamed it), and N/f.txt is new: released to pass 2. It was
// still taken for the row's file by the occupant check (pinned clobber), and
// pass 2 uploaded it too: "record and upload both act on N/f.txt", refused
// every cycle. F's row sits at N/f.txt once the folder commits, and is
// re-homed to N/h.txt only by its record in the transfer stage: it is dropped
// before g's move commits there (the record re-creates it by id).
func TestS031F2ReleasedFileIsKept(t *testing.T) {
	in := s031F1()
	in.Local["N/h.txt"] = locFile("X", 3)
	cp := "N/f" + conflictSuffix + ".txt"
	runPlan(t, in, []step{
		{t: MoveLocal, rel: "D", newRel: "N"},
		{t: ConflictBackup, rel: "N/f.txt", newRel: cp},
		{t: Forget, rel: "D/f.txt", fileID: "F"},
		{t: MoveLocal, rel: "g.txt", newRel: "N/f.txt"},
		{t: Upload, rel: cp},
		{t: Record, rel: "N/f.txt", fileID: "G"},
		{t: Record, rel: "N/h.txt", fileID: "F"},
	})
	noWriteOver(t, in, "F")
}

// F3: the folder's commit depends on the forget — if the forget's DB write
// fails, the commit would re-home the row onto the stranger's file and the
// next cycle upload it over Drive's. The commit is ProtectedBy the forget; a
// second forget is ProtectedBy the first, so any failure reaches the commit.
func TestS031F3FolderCommitIsProtectedByTheForgets(t *testing.T) {
	in := Input{
		Base:  map[string]BaseItem{"D": baseDir("FD"), "D/e.txt": baseFile("E", "Y", 3), "D/f.txt": baseFile("F", "X", 3)},
		Local: map[string]LocalItem{"N": locDir(), "N/e.txt": locFileM("P", 6, 2000), "N/f.txt": locFileM("Q", 5, 2000)},
		Remote: map[string]RemoteItem{"N": remDir("FD", 2), "N/e.txt": remFile("E", "Y", 3, 1),
			"N/f.txt": remFile("F", "X", 3, 1)},
	}
	runPlan(t, in, []step{
		{t: Forget, rel: "D/e.txt", fileID: "E"},
		{t: Forget, rel: "D/f.txt", fileID: "F"},
		{t: MoveLocal, rel: "D", newRel: "N"},
	})
	plan := planOf(in)
	if plan[1].ProtectedBy != "D/e.txt" || plan[2].ProtectedBy != "D/f.txt" {
		t.Errorf("protectors: second forget %q, folder commit %q; want D/e.txt, D/f.txt",
			plan[1].ProtectedBy, plan[2].ProtectedBy)
	}
}

// Drive holds two items named x.txt (G moved beside X); the snapshot keeps
// G, so X's row is held (§5) — and its file here was edited. G's move onto
// x.txt waits with it: renaming over the held row's file loses the edit, and
// backing it up leaves X's row at the name, which the move's commit needs.
// Was: the move was pinned to the edit's stat and renamed over it.
func TestS031MoveOntoAHeldRowsFileWaits(t *testing.T) {
	in := Input{
		Base:           map[string]BaseItem{"x.txt": baseFile("X", "MX", 3), "g.txt": baseFile("G", "MG", 4)},
		Local:          map[string]LocalItem{"x.txt": locFileM("EDIT", 9, 2000), "g.txt": locFile("MG", 4)},
		Remote:         map[string]RemoteItem{"x.txt": remFile("G", "MG", 4, 2)},
		ShadowedRemote: map[string]bool{"X": true},
	}
	runPlan(t, in, nil)
	in.Machine, in.Now = "test_box", testNow
	if _, skips := Plan(in); len(skips) != 2 || skips[0].RelPath != "g.txt" || skips[1].RelPath != "x.txt" {
		t.Errorf("skips = %+v, want g.txt and x.txt reported", skips)
	}
}

func planOf(in Input) []Action {
	in.Machine, in.Now = "test_box", testNow
	plan, _ := Plan(in)
	return plan
}

// noPinnedMoves: no move in the plan lands on an occupant — each destination
// is vacated by an earlier action, and the move expects it empty.
func noPinnedMoves(t *testing.T, in Input) {
	t.Helper()
	for _, a := range planOf(in) {
		if a.Type == MoveLocal && a.LocalExists {
			t.Errorf("%s %q → %q is pinned to an occupant (size %d, mtime %d)", a.Type, a.RelPath, a.NewRelPath, a.LocalSize, a.LocalMtimeNS)
		}
		if a.Type == MoveLocal && strings.Contains(a.NewRelPath, "conflict") && a.ProtectedBy != "" {
			t.Errorf("the move to the temporary name %q depends on %q; it must depend on nothing", a.NewRelPath, a.ProtectedBy)
		}
	}
}

// A move cut between its rename and its commit (fuzz seed 19 at
// SYNCKEEPER_FUZZ_CRASH=1, through the new move_before_commit checkpoint), then
// a new file written at the old name: the row's file sits at Drive's path
// with its baseline bytes, the old path holds a stranger. The file at Drive's
// path is the row's (R25); the stranger is new — uploaded in the same cycle
// (round 2; round 1 waited a cycle for the row to leave its path). Pre-existing (same plans on e27c8bc): with
// Drive edited, a true conflict at the old path plus pass 2's upload at the
// new one — §4.5 refused every cycle; with Drive unchanged, the stranger went
// up into the row's Drive item and the row's own bytes to a conflict copy.
func TestS031InterruptedMoveThenNewFileAtTheOldName(t *testing.T) {
	in := func(drive string) Input {
		return Input{
			Base:   map[string]BaseItem{"f.txt": baseFile("F", "MF", 3)},
			Local:  map[string]LocalItem{"f.txt": locFileM("NEW", 9, 3000), "g.txt": locFile("MF", 3)},
			Remote: map[string]RemoteItem{"g.txt": remFile("F", drive, 4, 2)},
		}
	}
	runPlan(t, in("MD"), []step{{t: Upload, rel: "f.txt"}, {t: Download, rel: "g.txt", fileID: "F"}})
	runPlan(t, in("MF"), []step{{t: Upload, rel: "f.txt"}, {t: Record, rel: "g.txt", fileID: "F"}})
	noWriteOver(t, in("MF"), "F")
}

// Round 2 (independent check): renamed c.txt → b.txt on both sides and a new
// c.txt created on both — here the only copy of new bytes, on Drive a new item.
// The release (S031.16) makes the local c.txt new, but its row stayed in Base,
// so pass 2 skipped it and pass 3 pinned R2's download to it: rule O1 refused
// the plan every cycle. The two c.txt are two new files at one path — §4.2's
// "created on both sides": same bytes → adopt; else the local one is the
// conflict copy (uploaded) and Drive's comes down at the name.
func s031RotationBothSides(local string) Input {
	return Input{
		Base:   map[string]BaseItem{"c.txt": baseFile("I1", "B1", 3)},
		Local:  map[string]LocalItem{"b.txt": locFile("B1", 3), "c.txt": locFileM(local, 9, 3000)},
		Remote: map[string]RemoteItem{"b.txt": remFile("I1", "B1", 3, 2), "c.txt": remFile("R2", "RN", 7, 1)},
	}
}

func TestS031RotationOnBothSidesNewFilesMeetAsBothNew(t *testing.T) {
	cp := "c" + conflictSuffix + ".txt"
	runPlan(t, s031RotationBothSides("Q"), []step{
		{t: ConflictBackup, rel: "c.txt", newRel: cp},
		{t: Record, rel: "b.txt", fileID: "I1"},
		{t: Upload, rel: cp},
		{t: Download, rel: "c.txt", fileID: "R2"},
	})
	runPlan(t, s031RotationBothSides("RN"), []step{
		{t: Record, rel: "b.txt", fileID: "I1"},
		{t: Record, rel: "c.txt", fileID: "R2"},
	})
}

// Pre-existing, found by the random sweep (TestPlanFuzz): a.txt edited here
// while Drive moved it to c.txt and edited it too (a true conflict), and a new
// c.txt made here. The download of Drive's bytes to c.txt met the new file,
// which pass 2 also uploaded: §4.5 refused every cycle. The occupant is kept
// like a move's — a conflict copy first — and the download expects c.txt empty.
func TestS031TrueConflictDownloadKeepsTheOccupant(t *testing.T) {
	cpA, cpC := "a"+conflictSuffix+".txt", "c"+conflictSuffix+".txt"
	runPlan(t, Input{
		Base:   map[string]BaseItem{"a.txt": baseFile("I0", "B0", 10)},
		Local:  map[string]LocalItem{"a.txt": locFileM("LI0", 60, 2001), "c.txt": locFileM("N2", 302, 2003)},
		Remote: map[string]RemoteItem{"c.txt": remFile("I0", "DI0", 110, 2)},
	}, []step{
		{t: ConflictBackup, rel: "a.txt", newRel: cpA},
		{t: ConflictBackup, rel: "c.txt", newRel: cpC},
		{t: Upload, rel: cpA},
		{t: Upload, rel: cpC},
		{t: Download, rel: "c.txt", fileID: "I0"},
	})
}
