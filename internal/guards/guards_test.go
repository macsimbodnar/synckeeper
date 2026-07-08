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
