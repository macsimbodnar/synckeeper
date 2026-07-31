package guards

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// ErrMassDelete wraps the mass-delete guard failure so callers (the watch
// daemon's status recorder) can tell a guard block from any other error and
// surface the actionable "--confirm-deletes" hint.
var ErrMassDelete = errors.New("mass-delete threshold exceeded")

// EnsureSyncDir prepares the sync dir for a cycle, and reports whether it had
// to be recreated (W18-D).
//
// **A missing sync folder is never a deletion.** It is recreated and its
// content comes back from Drive; the caller resets the baseline so §11 makes a
// delete structurally impossible on the cycle that follows. This replaces the
// old hard error, which refused the cycle and waited for a human.
//
// **An emptied sync folder IS a deletion, and propagates.** The old guard also
// refused when the directory existed but was empty while the baseline tracked
// items, on the theory that it meant "unmounted". That arm is gone: deleting
// everything inside your sync folder is a legitimate deletion and reaches
// Drive's bin through §4.2's ordinary `present | deleted | unchanged` row.
//
// The cost is recorded and accepted (decisions.md 2026-07-31): an unmounted
// volume whose mountpoint survives as an empty directory is indistinguishable
// from a folder the user emptied, so mounting *only* the sync folder from a
// separate volume is an unsupported configuration (MANUAL §9).
//
// Unreadable, or not a directory at all, stay hard errors: neither is a
// deletion, and neither is something to paper over by creating a directory.
func EnsureSyncDir(dir string, trackedItems int) (recreated bool, err error) {
	info, err := os.Stat(dir)
	switch {
	case err == nil && info.IsDir():
		if _, err := os.ReadDir(dir); err != nil {
			return false, fmt.Errorf("sync dir %s is unreadable: %w", dir, err)
		}
		return false, nil
	case err == nil:
		return false, fmt.Errorf("sync dir %s is not a directory", dir)
	case !os.IsNotExist(err):
		return false, fmt.Errorf("sync dir %s is unreadable: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("recreate sync dir %s: %w", dir, err)
	}
	if trackedItems > 0 {
		slog.Warn("the sync folder was gone; recreated it and the content will be downloaded again (nothing is deleted)",
			"sync_dir", dir, "tracked", trackedItems)
	}
	return true, nil
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
// (A2). EnsureSyncDir is a different guard and is not affected by any of this:
// a missing sync dir is recreated rather than counted, and an unreadable one
// is a hard error — neither is ever a deletion (W18-D).
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
