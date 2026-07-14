package guards

import (
	"errors"
	"fmt"
	"os"

	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// ErrMassDelete wraps the mass-delete guard failure so callers (the watch
// daemon's status recorder) can tell a guard block from any other error and
// surface the actionable "--confirm-deletes" hint.
var ErrMassDelete = errors.New("mass-delete threshold exceeded")

// CheckSyncDir hard-errors when the sync dir is missing, unreadable, or
// empty while the baseline tracks items — those states mean "the disk went
// away", never "the user deleted everything".
func CheckSyncDir(dir string, trackedItems int) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("sync dir %s is missing or unreadable (refusing to treat this as a mass delete): %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("sync dir %s is not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("sync dir %s is unreadable: %w", dir, err)
	}
	if len(entries) == 0 && trackedItems > 0 {
		return fmt.Errorf("sync dir %s is empty but the state DB tracks %d items — looks unmounted or wiped; refusing to propagate deletions (restore the dir or re-init)", dir, trackedItems)
	}
	return nil
}

// CheckMassDelete refuses plans that delete more than threshold of tracked
// items (and more than 10 absolute) unless the user confirmed.
func CheckMassDelete(plan []reconcile.Action, trackedItems int, threshold float64, confirmed bool) error {
	if confirmed || trackedItems == 0 {
		return nil
	}
	deletions := 0
	for _, a := range plan {
		if a.Type == reconcile.TrashRemote || a.Type == reconcile.QuarantineLocal {
			deletions++
		}
	}
	if deletions > 10 && float64(deletions)/float64(trackedItems) > threshold {
		return fmt.Errorf("plan deletes %d of %d tracked items (over the %.0f%% mass-delete threshold); re-run with --confirm-deletes if intended: %w",
			deletions, trackedItems, threshold*100, ErrMassDelete)
	}
	return nil
}
