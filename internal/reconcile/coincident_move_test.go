package reconcile

import "testing"

// R25 (found by the W4 fuzzer): a baseline file whose remote moved to a new
// path, with the local file already sitting at that new path — the user made
// the same rename, or a crashed MoveLocal left the disk and Drive ahead of the
// baseline. The file is alive on both sides at the new path, not locally
// deleted. Was: pass 1 planned a Download to "restore" the remote file at the
// new path while pass 2 uploaded the "new" local file at the same path, and
// §4.5 refused the whole plan every cycle — a permanent wedge (runPlan's
// property net catches the overlap even before the assertions below).

func TestR25CoincidentMoveRecordsInPlace(t *testing.T) {
	in := Input{
		Base:   map[string]BaseItem{"old/p.txt": baseFile("F", "X", 3)},
		Local:  map[string]LocalItem{"new/q.txt": locFile("X", 3)},
		Remote: map[string]RemoteItem{"new/q.txt": remFile("F", "X", 3, 2)},
	}
	// Same content on both sides at the new path: just record the row there
	// (UpsertItem is keyed on the file id, so the stale old/p.txt row is
	// dropped). No transfer, no upload/download collision.
	runPlan(t, in, []step{
		{t: Record, rel: "new/q.txt", fileID: "F"},
	})
}

func TestR25CoincidentMoveDivergentContentConflicts(t *testing.T) {
	in := Input{
		Base:   map[string]BaseItem{"old/p.txt": baseFile("F", "X", 3)},
		Local:  map[string]LocalItem{"new/q.txt": locFile("Y", 3)}, // edited at the new path
		Remote: map[string]RemoteItem{"new/q.txt": remFile("F", "X", 3, 2)},
	}
	// Diverged at the shared destination: the local bytes are preserved as a
	// conflict copy, the remote wins the canonical path. The conflict copy
	// sorts before "new/q.txt" (space < dot), so the two transfers act on
	// distinct paths and §4.5 is satisfied.
	cp := "new/q (conflict test_box 2026-07-08_103000).txt"
	runPlan(t, in, []step{
		{t: ConflictBackup, rel: "new/q.txt", newRel: cp},
		{t: Upload, rel: cp},
		{t: Download, rel: "new/q.txt", fileID: "F"},
	})
}
