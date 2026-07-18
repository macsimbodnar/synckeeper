package reconcile

// R12 (spec §4.5 made executable): the transfer stage — the only stage the
// executor runs concurrently — never holds two actions on one rel_path or
// on a file-level ancestor/descendant pair. Serial stages sequence
// overlapping paths by design and are exempt (scoped 2026-07-18, plan
// review); a dir Record commits a DB row only and cannot race a child's
// file I/O, so it is exempt from the ancestor rule (not the same-path one).

import "testing"

func TestR12ValidateRefusesSamePathTransfers(t *testing.T) {
	plan := []Action{
		{Type: Upload, RelPath: "B/x"},
		{Type: Download, RelPath: "B/x", FileID: "f2"},
	}
	if err := ValidateTransferStage(plan); err == nil {
		t.Error("upload + download on one rel_path must refuse the plan")
	}
}

func TestR12ValidateRefusesAncestorDescendantFiles(t *testing.T) {
	plan := []Action{
		{Type: Download, RelPath: "a/b"},
		{Type: Upload, RelPath: "a/b/c"},
	}
	if err := ValidateTransferStage(plan); err == nil {
		t.Error("file transfers on an ancestor/descendant pair must refuse the plan")
	}
}

func TestR12ValidateAllowsLegitimatePlans(t *testing.T) {
	// A dir adopt Record plus a child download is a real, correct plan.
	adopt := []Action{
		{Type: Record, RelPath: "shared", IsDir: true},
		{Type: Download, RelPath: "shared/f.txt"},
	}
	if err := ValidateTransferStage(adopt); err != nil {
		t.Errorf("dir Record + child transfer is legitimate: %v", err)
	}
	// Serial stages sequence overlapping paths by design (R7's
	// backup-then-move); only transfers are checked.
	serial := []Action{
		{Type: ConflictBackup, RelPath: "p", NewRelPath: "p (conflict)"},
		{Type: MoveLocal, RelPath: "q", NewRelPath: "p"},
		{Type: Upload, RelPath: "p (conflict)"},
	}
	if err := ValidateTransferStage(serial); err != nil {
		t.Errorf("serial-stage overlap must not refuse: %v", err)
	}
}
