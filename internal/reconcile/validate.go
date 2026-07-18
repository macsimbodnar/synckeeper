package reconcile

// ValidateTransferStage enforces spec §4.5's concurrency rule mechanically
// (R12): the transfer stage — the only stage the executor runs
// concurrently — must not hold two actions on one rel_path, nor file-level
// actions on an ancestor/descendant pair. A planner mistake surfaces as a
// refused plan, not a race. Serial stages are exempt by design: their
// ordering is the mechanism (nested mkdirs, bottom-up deletes, R7's
// backup-then-move). A directory Record commits a DB row only and cannot
// race a child's file I/O, so it is exempt from the ancestor rule but not
// the same-path one.

import (
	"fmt"
	"strings"
)

// ValidateTransferStage returns an error if the plan's transfer stage
// violates the §4.5 no-overlap rule. The executor refuses such a plan
// before executing anything.
func ValidateTransferStage(plan []Action) error {
	seen := map[string]Type{}
	fileSet := map[string]bool{}
	for _, a := range plan {
		switch a.Type {
		case Upload, UpdateRemote, Download, Record:
		default:
			continue
		}
		if prev, dup := seen[a.RelPath]; dup {
			return fmt.Errorf("plan violates §4.5: %s and %s both act on %q in the concurrent transfer stage; refusing the plan, replanning next cycle", prev, a.Type, a.RelPath)
		}
		seen[a.RelPath] = a.Type
		if !a.IsDir {
			fileSet[a.RelPath] = true
		}
	}
	for p := range fileSet {
		for i := strings.LastIndexByte(p, '/'); i > 0; i = strings.LastIndexByte(p[:i], '/') {
			if fileSet[p[:i]] {
				return fmt.Errorf("plan violates §4.5: transfer-stage actions on ancestor/descendant pair %q and %q; refusing the plan, replanning next cycle", p[:i], p)
			}
		}
	}
	return nil
}
