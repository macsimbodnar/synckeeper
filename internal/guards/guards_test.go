package guards

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

func TestCheckSyncDir(t *testing.T) {
	dir := t.TempDir()

	if err := CheckSyncDir(dir, 0); err != nil {
		t.Errorf("empty dir with empty DB should pass: %v", err)
	}
	if err := CheckSyncDir(dir, 5); err == nil {
		t.Error("empty dir with populated DB must hard-error (G2)")
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckSyncDir(dir, 5); err != nil {
		t.Errorf("non-empty dir should pass: %v", err)
	}
	if err := CheckSyncDir(filepath.Join(dir, "missing"), 0); err == nil {
		t.Error("missing dir must hard-error even with empty DB")
	}
}

// deletionPlan is n local removals — the direction the guard still watches
// when there is nowhere recoverable to put them.
func deletionPlan(n int) []reconcile.Action {
	plan := make([]reconcile.Action, n)
	for i := range plan {
		plan[i] = reconcile.Action{Type: reconcile.QuarantineLocal}
	}
	return plan
}

// noBin / withBin name the argument at the call sites, since the whole guard
// now turns on it (W14-M1).
const (
	noBin   = false
	withBin = true
)

func TestCheckMassDelete(t *testing.T) {
	// 50 of 100 tracked, nowhere recoverable to put them -> blocked (G1).
	if err := CheckMassDelete(deletionPlan(50), 100, 0.25, false, noBin); err == nil {
		t.Error("50% deletion into the quarantine must be blocked without confirmation")
	}
	// Same with confirmation -> allowed.
	if err := CheckMassDelete(deletionPlan(50), 100, 0.25, true, noBin); err != nil {
		t.Errorf("confirmed deletion should pass: %v", err)
	}
	// 8 of 10 is a huge fraction but under the absolute floor of 10.
	if err := CheckMassDelete(deletionPlan(8), 10, 0.25, false, noBin); err != nil {
		t.Errorf("small absolute deletions should pass: %v", err)
	}
	// 20 of 1000 is over 10 absolute but a tiny fraction.
	if err := CheckMassDelete(deletionPlan(20), 1000, 0.25, false, noBin); err != nil {
		t.Errorf("small fraction should pass: %v", err)
	}
}

// W14-M1, the change itself: the guard's question is "can the user get this
// back?", not "is this a lot?". With a system bin to receive them, a deletion
// of every tracked file passes without a question — it is one restore away.
func TestMassDeleteUnguardedWhenRecoverable(t *testing.T) {
	if err := CheckMassDelete(deletionPlan(100), 100, 0.25, false, withBin); err != nil {
		t.Errorf("deletions into the system bin must never be blocked: %v", err)
	}
	// A collapsed folder standing for the whole tree, likewise.
	collapsed := []reconcile.Action{{Type: reconcile.QuarantineLocal, RelPath: "pack", IsDir: true, SubtreeFiles: 1145}}
	if err := CheckMassDelete(collapsed, 1146, 0.25, false, withBin); err != nil {
		t.Errorf("a collapsed folder into the system bin must not be blocked: %v", err)
	}
}

// Drive's bin is always the destination on the remote side, so a local
// deletion propagating to Drive is never guarded — whatever this machine can
// or cannot offer locally.
func TestMassDeleteNeverGuardsTheDriveSide(t *testing.T) {
	remote := make([]reconcile.Action, 500)
	for i := range remote {
		remote[i] = reconcile.Action{Type: reconcile.TrashRemote}
	}
	for _, bin := range []bool{withBin, noBin} {
		if err := CheckMassDelete(remote, 500, 0.25, false, bin); err != nil {
			t.Errorf("bin=%v: trashing on Drive must never be blocked (Drive's own bin holds it): %v", bin, err)
		}
	}
	// Mixed: only the local half counts, and only without a bin.
	mixed := append(remote, deletionPlan(50)...)
	if err := CheckMassDelete(mixed, 100, 0.25, false, noBin); err == nil {
		t.Error("the local half of a mixed plan must still be counted when there is no bin")
	}
}

func dirDeletionPlan(n int) []reconcile.Action {
	plan := make([]reconcile.Action, n)
	for i := range plan {
		plan[i] = reconcile.Action{Type: reconcile.QuarantineLocal, IsDir: true}
	}
	return plan
}

// R10 (spec §6, G4): the guard counts content, not containers — directory
// deletions are excluded from the count, and the denominator is tracked
// FILES. An empty folder disappearing is not the loss the guard exists to
// catch, and counting containers wedged the daemon on ordinary folder
// reorganisation (A2). Unchanged by W14 for the case that still guards.
func TestR10GuardCountsContentNotContainers(t *testing.T) {
	// 21 directory deletions, zero file deletions: never a mass delete.
	if err := CheckMassDelete(dirDeletionPlan(21), 21, 0.25, false, noBin); err != nil {
		t.Errorf("directory-only deletions must not trip the guard: %v", err)
	}
	// Mixed plan: the 12 file deletions still trip against 20 tracked files,
	// regardless of how many containers go with them.
	mixed := append(dirDeletionPlan(30), deletionPlan(12)...)
	if err := CheckMassDelete(mixed, 20, 0.25, false, noBin); err == nil {
		t.Error("12 file deletions of 20 tracked files must trip the guard")
	}
	// 11 file deletions of 100 tracked files: over the absolute floor but a
	// small fraction — allowed, dirs still ignored.
	small := append(dirDeletionPlan(40), deletionPlan(11)...)
	if err := CheckMassDelete(small, 100, 0.25, false, noBin); err != nil {
		t.Errorf("small file fraction must pass regardless of dir count: %v", err)
	}
}

// T13.5 (W13-T2, A2's mirror image): a directory delete that absorbed its
// whole subtree stands for the files inside it. Counting the container as one
// deletion — or as none — would wave a 1145-file folder straight past the
// guard in the one case that still guards.
func TestT13GuardCountsACollapsedSubtreesFiles(t *testing.T) {
	collapsed := []reconcile.Action{
		{Type: reconcile.QuarantineLocal, RelPath: "pack", IsDir: true, SubtreeFiles: 50},
	}
	if err := CheckMassDelete(collapsed, 100, 0.25, false, noBin); err == nil {
		t.Error("a collapsed 50-file folder must trip the guard, not read as zero deletions")
	}
	// The R10 rule is unchanged for a directory that stands for nothing.
	if err := CheckMassDelete(dirDeletionPlan(21), 21, 0.25, false, noBin); err != nil {
		t.Errorf("an uncollapsed directory delete must still be a container, not content: %v", err)
	}
}
