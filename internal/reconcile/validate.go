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

	"github.com/macsimbodnar/synckeeper/internal/names"
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

// RefusedPlan is a plan-level refusal: a planner mistake caught before the
// plan executes. Nothing runs and no state changes, so the next cycle plans
// the same and is refused again — the daemon counts these as a standstill
// (S002), whichever check refused.
type RefusedPlan interface {
	error
	RefusedPaths() []string
}

// RefusedPaths names the path(s) the refusal is about.
func (e *TransferOverlapError) RefusedPaths() []string { return e.Paths }

// UnkeptOverwriteError is ValidateLocalOverwrites' refusal (§4.5, rule O1).
type UnkeptOverwriteError struct {
	Paths []string // where the action lands, and the local file it would destroy
	msg   string
}

func (e *UnkeptOverwriteError) Error() string { return e.msg }

// RefusedPaths names the target and the local file the plan would destroy.
func (e *UnkeptOverwriteError) RefusedPaths() []string { return e.Paths }

// ValidateLocalOverwrites enforces spec §4.5 rule O1 (S031): no action may
// replace a local file whose bytes are not provably kept. It is the net under
// every occupant rule of the planner — the moves stage once renamed one file
// over another's unsynced edit because two moves ran in path order and the
// edited occupant was taken for "safe on Drive".
//
// The actions that can replace a local file are the ones the local-write gate
// lets land on an occupant: a file MoveLocal or a Download carrying a pinned
// stat (LocalExists). Without a pin the gate refuses any occupant, and a
// ConflictBackup always expects its destination empty. A QuarantineLocal
// does not destroy — it bins — but binning an edit breaks §4.2's "edit beats
// delete", so it is held to its row's baseline bytes (each covered file of a
// collapsed folder delete likewise).
//
// The check simulates the plan in order on the scan's local files. Every
// earlier action may fail (I/O, a guard, a failed protector), so each path
// holds the set of files that MAY be there, each with the actions that must
// have failed for it to be. An action runs only if every protector it is
// ProtectedBy succeeded (the executor's failedProtectors, transitively), so
// those protectors' failures are excluded. A possible occupant whose scanned
// stat equals the action's pin is destroyed if the action runs (the gate
// passes it); it must be kept:
//
//   - its bytes are content Drive holds now (some Drive item's md5), or held
//     at the last sync (a baseline row whose content md5 equals its Drive
//     md5) — then Drive keeps them, as the current content, an earlier
//     revision, or in its bin; or
//   - it is bytes a download of this plan just wrote.
//
// What it cannot see: a racing write (the gate's job); a stat pin that
// matches a different file by coincidence of size and mtime (counted as that
// file, so it refuses rather than passes); whether Drive's copy outlives its
// revision or bin retention (§3.3's bound); and identity — a kept file
// replaced by another file's bytes loses nothing here, but the plan may still
// bind the wrong bytes to an item (the stat pins on Records and the moves'
// ProtectedBy are that net).
func ValidateLocalOverwrites(in Input, plan []Action) error {
	key := func(p string) string { return p }
	if in.CaseFold || in.NormFold {
		key = func(p string) string {
			segs := strings.Split(p, "/")
			for i, s := range segs {
				segs[i] = names.FoldKey(s, in.CaseFold, in.NormFold)
			}
			return strings.Join(segs, "/")
		}
	}
	onDrive := map[string]bool{}
	baseByID := map[string]BaseItem{}
	for _, b := range in.Base {
		baseByID[b.FileID] = b
		if !b.IsDir && b.MD5 != "" && b.MD5 == b.DriveMD5 {
			onDrive[b.MD5] = true
		}
	}
	for _, r := range in.Remote {
		if !r.IsDir && r.MD5 != "" {
			onDrive[r.MD5] = true
		}
	}

	// A possible occupant: a scanned local file (origin, its scan path) or
	// bytes a download wrote (origin ""), there only if every action in
	// mustFail failed.
	type entry struct {
		origin   string
		mustFail []int
	}
	occ := map[string][]entry{}
	for _, p := range slices.Sorted(maps.Keys(in.Local)) {
		if !in.Local[p].IsDir {
			occ[key(p)] = append(occ[key(p)], entry{origin: p})
		}
	}
	unless := func(es []entry, i int) []entry {
		out := make([]entry, len(es))
		for k, e := range es {
			out[k] = entry{e.origin, append(slices.Clip(e.mustFail), i)}
		}
		return out
	}
	moveOne := func(src, dst string, i int) {
		moved := occ[src]
		occ[dst] = append(unless(occ[dst], i), moved...)
		occ[src] = unless(moved, i)
	}
	under := func(dir string) []string {
		var out []string
		for k := range occ {
			if strings.HasPrefix(k, dir+"/") {
				out = append(out, k)
			}
		}
		slices.Sort(out)
		return out
	}

	isProtector := func(t Type) bool {
		switch t {
		case ConflictBackup, MoveLocal, MoveRemote, Forget:
			return true
		}
		return false
	}
	protectors := map[string][]int{} // RelPath → earlier protector-type actions
	forced := make([]map[int]bool, len(plan))
	destroys := func(i int, a Action, at string, pinned bool, kept func(LocalItem) bool, what string) error {
		for _, e := range occ[key(at)] {
			if e.origin == "" || slices.ContainsFunc(e.mustFail, func(j int) bool { return forced[i][j] }) {
				continue
			}
			l := in.Local[e.origin]
			if pinned && (l.Size != a.LocalSize || l.MtimeNS != a.LocalMtimeNS) {
				continue // the gate refuses: not the file the pin names
			}
			if !kept(l) {
				return &UnkeptOverwriteError{Paths: []string{at, e.origin},
					msg: fmt.Sprintf("plan violates §4.5 rule O1: %s %q would %s %q (md5 %s), whose bytes are not kept; refusing the plan, replanning next cycle",
						a.Type, a.RelPath, what, e.origin, l.MD5)}
			}
		}
		return nil
	}
	notEdited := func(id string) func(LocalItem) bool {
		return func(l LocalItem) bool { b, ok := baseByID[id]; return ok && l.MD5 == b.MD5 }
	}
	kept := func(l LocalItem) bool { return onDrive[l.MD5] }

	for i, a := range plan {
		if a.ProtectedBy != "" {
			forced[i] = map[int]bool{}
			for _, j := range protectors[a.ProtectedBy] {
				forced[i][j] = true
				maps.Copy(forced[i], forced[j])
			}
		}
		var err error
		switch {
		case a.Type == MoveLocal && !a.IsDir && a.LocalExists && key(a.RelPath) != key(a.NewRelPath):
			err = destroys(i, a, a.NewRelPath, true, kept, "replace")
		case a.Type == Download && a.LocalExists:
			err = destroys(i, a, a.RelPath, true, kept, "replace")
		case a.Type == QuarantineLocal && !a.IsDir:
			err = destroys(i, a, a.RelPath, a.LocalExists, notEdited(a.FileID), "bin the edit")
		case a.Type == QuarantineLocal:
			for _, s := range a.Subtree {
				if !s.IsDir && err == nil {
					sub := Action{Type: a.Type, RelPath: s.RelPath, LocalSize: s.Size, LocalMtimeNS: s.MtimeNS}
					err = destroys(i, sub, s.RelPath, true, notEdited(s.FileID), "bin the edit")
				}
			}
		}
		if err != nil {
			return err
		}

		switch a.Type {
		case MoveLocal, ConflictBackup:
			src, dst := key(a.RelPath), key(a.NewRelPath)
			switch {
			case src == dst: // a case-only rename: the file stays itself
			case a.IsDir:
				for _, k := range under(src) {
					moveOne(k, dst+k[len(src):], i)
				}
			default:
				moveOne(src, dst, i)
			}
		case QuarantineLocal:
			// A folder's removal is not applied to the files under it: the
			// delete stage runs last, and a removal only shrinks what may be
			// there — leaving it out keeps the check conservative and linear.
			if !a.IsDir {
				k := key(a.RelPath)
				occ[k] = unless(occ[k], i)
			}
		case Download:
			k := key(a.RelPath)
			occ[k] = append(unless(occ[k], i), entry{})
		}
		if isProtector(a.Type) {
			protectors[a.RelPath] = append(protectors[a.RelPath], i)
		}
	}
	return nil
}
