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

func deletionPlan(n int) []reconcile.Action {
	plan := make([]reconcile.Action, n)
	for i := range plan {
		plan[i] = reconcile.Action{Type: reconcile.TrashRemote}
	}
	return plan
}

func TestCheckMassDelete(t *testing.T) {
	// 50 of 100 tracked: over 25% and over 10 absolute -> blocked (G1).
	if err := CheckMassDelete(deletionPlan(50), 100, 0.25, false); err == nil {
		t.Error("50% deletion must be blocked without confirmation")
	}
	// Same with confirmation -> allowed.
	if err := CheckMassDelete(deletionPlan(50), 100, 0.25, true); err != nil {
		t.Errorf("confirmed deletion should pass: %v", err)
	}
	// 8 of 10 is a huge fraction but under the absolute floor of 10.
	if err := CheckMassDelete(deletionPlan(8), 10, 0.25, false); err != nil {
		t.Errorf("small absolute deletions should pass: %v", err)
	}
	// 20 of 1000 is over 10 absolute but a tiny fraction.
	if err := CheckMassDelete(deletionPlan(20), 1000, 0.25, false); err != nil {
		t.Errorf("small fraction should pass: %v", err)
	}
}

func dirDeletionPlan(n int) []reconcile.Action {
	plan := make([]reconcile.Action, n)
	for i := range plan {
		plan[i] = reconcile.Action{Type: reconcile.TrashRemote, IsDir: true}
	}
	return plan
}

// R10 (spec §6, G4): the guard counts content, not containers — directory
// deletions are excluded from the count, and the denominator is tracked
// FILES. An empty folder disappearing is not the loss the guard exists to
// catch, and counting containers wedged the daemon on ordinary folder
// reorganisation (A2).
func TestR10GuardCountsContentNotContainers(t *testing.T) {
	// 21 directory deletions, zero file deletions: never a mass delete.
	if err := CheckMassDelete(dirDeletionPlan(21), 21, 0.25, false); err != nil {
		t.Errorf("directory-only deletions must not trip the guard: %v", err)
	}
	// Mixed plan: the 12 file deletions still trip against 20 tracked files,
	// regardless of how many containers go with them.
	mixed := append(dirDeletionPlan(30), deletionPlan(12)...)
	if err := CheckMassDelete(mixed, 20, 0.25, false); err == nil {
		t.Error("12 file deletions of 20 tracked files must trip the guard")
	}
	// 11 file deletions of 100 tracked files: over the absolute floor but a
	// small fraction — allowed, dirs still ignored.
	small := append(dirDeletionPlan(40), deletionPlan(11)...)
	if err := CheckMassDelete(small, 100, 0.25, false); err != nil {
		t.Errorf("small file fraction must pass regardless of dir count: %v", err)
	}
}
