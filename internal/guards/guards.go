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

// CheckMassDelete refuses a plan whose deletions would land somewhere the
// user cannot restore them from. **The trigger is recoverability, not volume**
// (W14, 2026-07-26): deleting a lot is not suspicious when every deleted item
// is one gesture away in a bin the user can see.
//
//   - Deletions on Drive (TrashRemote) are never guarded: they go to Drive's
//     own bin, visible and restorable in the web UI.
//   - Deletions on this machine (QuarantineLocal) are guarded only when there
//     is no system bin to receive them (binAvailable false) — then the content
//     goes to the private dated quarantine, which the user does not see and
//     which purges itself after quarantine_retention_days. That is the one
//     case where a large deletion is worth a question.
//
// For the case that remains the old rule is unchanged: more than threshold of
// tracked files AND more than 10 absolute, counting content and not containers
// (spec §6, R10) — an empty folder disappearing is not the loss the guard
// exists to catch, and counting containers made an ordinary folder
// reorganisation abort the one-shot and wedge the daemon in a standing block
// (A2). CheckSyncDir is a different guard and is not affected by any of this:
// a missing, unreadable, or empty sync dir is never a deletion.
func CheckMassDelete(plan []reconcile.Action, trackedFiles int, threshold float64, confirmed, binAvailable bool) error {
	if confirmed || trackedFiles == 0 || binAvailable {
		return nil
	}
	deletions := 0
	for _, a := range plan {
		// Drive-side deletions are recoverable from Drive's bin whatever
		// this machine can offer, so they never count.
		if a.Type != reconcile.QuarantineLocal {
			continue
		}
		// A directory delete that absorbed its subtree (W13-T2) still stands
		// for every file inside it: count those, or a whole tree collapsed
		// into one action would read as zero deletions and walk straight
		// past the guard. The guard is called before the collapse for
		// exactly that reason; counting SubtreeFiles here means the order
		// can never silently become load-bearing again. An uncollapsed
		// directory carries zero and stays excluded (R10).
		if a.IsDir {
			deletions += a.SubtreeFiles
			continue
		}
		deletions++
	}
	if deletions > 10 && float64(deletions)/float64(trackedFiles) > threshold {
		return fmt.Errorf("plan removes %d of %d tracked files from this machine (over the %.0f%% threshold) and there is no system bin to rescue them to — they would go to the private quarantine instead; re-run with --confirm-deletes if intended: %w",
			deletions, trackedFiles, threshold*100, ErrMassDelete)
	}
	return nil
}
