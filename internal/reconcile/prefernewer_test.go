package reconcile

import "testing"

// W18.7 — the init merge names the conflict winner by last edit (spec §11).
// PreferNewer is scoped to that one caller, so every case here is paired with
// the same input planned WITHOUT it: steady-state §4.2 must keep remote-wins,
// which is deterministic without a clock.
//
// The local-wins shape is the mirror of the remote-wins one. Remote wins: the
// local file is renamed aside on disk (ConflictBackup) and uploaded under the
// conflict name, the Drive copy downloads onto the plain name. Local wins: the
// Drive file is renamed aside ON DRIVE (MoveRemote) and downloads under the
// conflict name, the local file uploads onto the plain name. The rename is not
// cosmetic — without it the upload would put two items under one name in one
// Drive folder, which is the W17 shape.

// remFileAt is remFile with Drive's modification stamp, which is the only
// thing PreferNewer reads.
func remFileAt(id, md5 string, size, version, modNS int64) RemoteItem {
	r := remFile(id, md5, size, version)
	r.ModifiedNS = modNS
	return r
}

const (
	older = 1_000
	newer = 2_000
)

// bothNew is the `absent | new | new, diff md5` row: nothing in the baseline,
// different content under one path on each side.
func bothNew(localMtime, remoteMod int64) Input {
	return Input{
		Base:   map[string]BaseItem{},
		Local:  map[string]LocalItem{"notes.txt": locFileM("local-md5", 5, localMtime)},
		Remote: map[string]RemoteItem{"notes.txt": remFileAt("r1", "remote-md5", 7, 1, remoteMod)},
	}
}

const conflictNotes = "notes (conflict test_box 2026-07-08_103000).txt"

// The case E exists for: the user's machine holds work newer than the copy
// sitting on Drive, and joining must not demote it to a conflict copy.
func TestInitMergeGivesThePlainNameToTheNewerSide(t *testing.T) {
	in := bothNew(newer, older)
	in.PreferNewer = true
	runPlan(t, in, []step{
		{t: MoveRemote, rel: "notes.txt", newRel: conflictNotes, fileID: "r1"},
		{t: Download, rel: conflictNotes, fileID: "r1"},
		{t: Upload, rel: "notes.txt"},
	})

	// Nothing is lost on either side, and nothing is deleted to achieve it.
	plan, _ := Plan(withTestIdentity(in))
	if got := deleteClassCount(plan); got != 0 {
		t.Fatalf("delete-class actions = %d, want 0", got)
	}
	// The upload must not run if the Drive-side rename failed: two items under
	// one name in one folder is exactly what W17 was about.
	for _, a := range plan {
		if a.Type == Upload && a.ProtectedBy != "notes.txt" {
			t.Errorf("upload of the winner is ProtectedBy %q, want the MoveRemote's path %q", a.ProtectedBy, "notes.txt")
		}
	}
}

// The same inputs the other way round: Drive holds the newer copy, so the
// answer is the ordinary one and the plan is byte-for-byte today's.
func TestInitMergeLeavesTheOlderLocalSideAsTheConflictCopy(t *testing.T) {
	in := bothNew(older, newer)
	in.PreferNewer = true
	runPlan(t, in, []step{
		{t: ConflictBackup, rel: "notes.txt", newRel: conflictNotes},
		{t: Upload, rel: conflictNotes},
		{t: Download, rel: "notes.txt", fileID: "r1"},
	})
}

// Steady state is untouched: the same "local is newer" input that flips the
// winner at an init merge must not flip it during ordinary syncing, because
// remote-wins is the rule that needs no cross-machine clock agreement.
func TestSteadyStateIgnoresWhichSideIsNewer(t *testing.T) {
	in := bothNew(newer, older) // PreferNewer left false
	runPlan(t, in, []step{
		{t: ConflictBackup, rel: "notes.txt", newRel: conflictNotes},
		{t: Upload, rel: conflictNotes},
		{t: Download, rel: "notes.txt", fileID: "r1"},
	})
}

// An unknown Drive stamp — a mirror row written before schema v5, or a reply
// without modifiedTime — falls back to remote-wins rather than reading 0 as
// "the beginning of time" and handing every plain name to the local side.
// This is what makes the migration safe without a backfill.
func TestUnknownRemoteStampFallsBackToRemoteWins(t *testing.T) {
	in := bothNew(newer, 0)
	in.PreferNewer = true
	runPlan(t, in, []step{
		{t: ConflictBackup, rel: "notes.txt", newRel: conflictNotes},
		{t: Upload, rel: conflictNotes},
		{t: Download, rel: "notes.txt", fileID: "r1"},
	})
}

// A tie goes to Drive, for the same reason the default does: it is the answer
// that does not depend on whose clock is right.
func TestEqualStampsKeepRemoteWins(t *testing.T) {
	in := bothNew(newer, newer)
	in.PreferNewer = true
	runPlan(t, in, []step{
		{t: ConflictBackup, rel: "notes.txt", newRel: conflictNotes},
		{t: Upload, rel: conflictNotes},
		{t: Download, rel: "notes.txt", fileID: "r1"},
	})
}

// Identical content still adopts. Newer-wins decides a NAME, and there is no
// name to argue about when both sides hold the same bytes.
func TestNewerSideDoesNotConflictIdenticalContent(t *testing.T) {
	in := Input{
		Base:   map[string]BaseItem{},
		Local:  map[string]LocalItem{"notes.txt": locFileM("same", 5, newer)},
		Remote: map[string]RemoteItem{"notes.txt": remFileAt("r1", "same", 5, 1, older)},
	}
	in.PreferNewer = true
	runPlan(t, in, []step{{t: Record, rel: "notes.txt", fileID: "r1"}})
}

// A conflict INSIDE a folder that the same merge is adopting. The Drive-side
// rename needs the folder's id in the moves stage, which runs before the
// Record that adopts it — the plan carries that id, so the executor can seed
// it (see TestFolderIDsComeFromThePlanNotTheBaseline).
func TestNewerSideWinsInsideAnAdoptedFolder(t *testing.T) {
	in := Input{
		Base: map[string]BaseItem{},
		Local: map[string]LocalItem{
			"docs":           locDir(),
			"docs/notes.txt": locFileM("local-md5", 5, newer),
		},
		Remote: map[string]RemoteItem{
			"docs":           remDir("rdir", 1),
			"docs/notes.txt": remFileAt("r1", "remote-md5", 7, 1, older),
		},
	}
	in.PreferNewer = true
	cp := "docs/notes (conflict test_box 2026-07-08_103000).txt"
	runPlan(t, in, []step{
		{t: MoveRemote, rel: "docs/notes.txt", newRel: cp, fileID: "r1"},
		{t: Record, rel: "docs", fileID: "rdir"},
		{t: Download, rel: cp, fileID: "r1"},
		{t: Upload, rel: "docs/notes.txt"},
	})
}

// The fold-equal arm (C2/R19) obeys the same rule, or the promise would be
// true for "notes.txt" vs "notes.txt" and quietly false for "Notes.txt" vs
// "notes.txt". The loser steps aside from ITS OWN path — the Drive byte form.
func TestNewerSideWinsAcrossAFoldCollision(t *testing.T) {
	in := Input{
		Base:     map[string]BaseItem{},
		Local:    map[string]LocalItem{"Notes.txt": locFileM("local-md5", 5, newer)},
		Remote:   map[string]RemoteItem{"notes.txt": remFileAt("r1", "remote-md5", 7, 1, older)},
		CaseFold: true,
	}
	in.PreferNewer = true
	runPlan(t, in, []step{
		{t: MoveRemote, rel: "notes.txt", newRel: conflictNotes, fileID: "r1"},
		{t: Upload, rel: "Notes.txt"},
		{t: Download, rel: conflictNotes, fileID: "r1"},
	})
}

// withTestIdentity applies what runPlan applies, for the cases that also want
// to inspect the plan directly.
func withTestIdentity(in Input) Input {
	in.Machine = "test_box"
	in.Now = testNow
	return in
}
