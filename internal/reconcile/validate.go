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
	"maps"
	"slices"
	"strings"
)

// TransferOverlapError is ValidateTransferStage's refusal. It is a type so
// the daemon can tell a refused plan from every other failure (S002): the
// refusal executes nothing and changes no state, so the next cycle plans the
// same thing and is refused again — a standstill `status` must name.
type TransferOverlapError struct {
	Paths []string // the one rel_path, or the ancestor and the descendant
	msg   string
}

func (e *TransferOverlapError) Error() string { return e.msg }

// ValidateTransferStage returns an error if the plan's transfer stage
// violates the §4.5 no-overlap rule — always a *TransferOverlapError. The
// executor refuses such a plan before executing anything.
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
			return &TransferOverlapError{Paths: []string{a.RelPath},
				msg: fmt.Sprintf("plan violates §4.5: %s and %s both act on %q in the concurrent transfer stage; refusing the plan, replanning next cycle", prev, a.Type, a.RelPath)}
		}
		seen[a.RelPath] = a.Type
		if !a.IsDir {
			fileSet[a.RelPath] = true
		}
	}
	// Sorted, so a plan refused twice is refused with the same message: the
	// daemon compares it across cycles to tell a standstill (S002).
	for _, p := range slices.Sorted(maps.Keys(fileSet)) {
		for i := strings.LastIndexByte(p, '/'); i > 0; i = strings.LastIndexByte(p[:i], '/') {
			if fileSet[p[:i]] {
				return &TransferOverlapError{Paths: []string{p[:i], p},
					msg: fmt.Sprintf("plan violates §4.5: transfer-stage actions on ancestor/descendant pair %q and %q; refusing the plan, replanning next cycle", p[:i], p)}
			}
		}
	}
	return nil
}
