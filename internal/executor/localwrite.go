package executor

// The local-write gate (spec §7): this file is the ONLY file in the package
// permitted to mutate a local path. Every primitive that lands on the local
// tree states what it expects to find there, reality is re-statted
// immediately before the mutation, and any drift refuses the action — a
// racing local write always wins the cycle (invariant 7). The companion AST
// test (gate_test.go) enforces the exclusivity mechanically; the check had
// previously been added four separate times (R4/R6/R7/R8) and the fifth
// primitive was still unguarded, which is exactly why this is one choke
// point and not a convention.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/macsimbodnar/synckeeper/internal/names"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/trash"
)

// expectation is what a mutation expects to find at the path it acts on.
// The zero value means "absent".
type expectation struct {
	exists  bool
	size    int64
	mtimeNS int64
}

// expFromAction lifts the plan's pinned local stat into an expectation —
// Action.LocalExists/LocalSize/LocalMtimeNS is the gate's wire format.
func expFromAction(a reconcile.Action) expectation {
	return expectation{exists: a.LocalExists, size: a.LocalSize, mtimeNS: a.LocalMtimeNS}
}

// vanishPolicy is what a guarded mutation does when the expectation says a
// file should be present and nothing is there. Downloads and records refuse
// (R4: one rule — reality must match the plan or the cycle replans); moves
// proceed (R7: a sibling move may legitimately have vacated the destination
// first, e.g. the R6 swap).
type vanishPolicy int

const (
	refuseVanished vanishPolicy = iota
	allowVanished
)

// guardedStat re-stats abs immediately before a mutation and verifies it
// against exp. It returns the info when a matching regular file is present,
// nil when the path is absent and absence is acceptable, and an error on
// any drift. A sub-microsecond race between this stat and the mutation
// remains and is accepted.
func guardedStat(abs, what string, exp expectation, onVanish vanishPolicy) (os.FileInfo, error) {
	cur, err := os.Lstat(abs)
	switch {
	case err == nil:
		if !exp.exists {
			return nil, fmt.Errorf("%s: a file appeared after the scan; leaving it alone, replanning next cycle", what)
		}
		if !cur.Mode().IsRegular() || cur.Size() != exp.size || cur.ModTime().UnixNano() != exp.mtimeNS {
			return nil, fmt.Errorf("%s changed since the scan (size %d mtime %d, scanned %d/%d); leaving it alone, replanning next cycle",
				what, cur.Size(), cur.ModTime().UnixNano(), exp.size, exp.mtimeNS)
		}
		return cur, nil
	case os.IsNotExist(err):
		if exp.exists && onVanish == refuseVanished {
			return nil, fmt.Errorf("%s disappeared after the scan; replanning next cycle", what)
		}
		return nil, nil
	default:
		return nil, err
	}
}

// guardedRename verifies the destination against exp, renames onto it, and
// makes the destination directory entry durable.
func guardedRename(from, to, what string, exp expectation, onVanish vanishPolicy) error {
	if _, err := guardedStat(to, what, exp, onVanish); err != nil {
		return err
	}
	if err := os.Rename(from, to); err != nil {
		return err
	}
	fsyncDir(filepath.Dir(to))
	return nil
}

// storedName is the name abs's last element is stored under in its parent
// directory. On a disk that folds case or accents a stat by another spelling
// finds the entry and does not tell its spelling (S038). An exact entry wins;
// else the one equal under the fold. "" when there is none.
func storedName(abs string) (string, error) {
	want := filepath.Base(abs)
	ents, err := os.ReadDir(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	key, found := names.FoldKey(want, true, true), ""
	for _, e := range ents {
		switch {
		case e.Name() == want:
			return want, nil
		case found == "" && names.FoldKey(e.Name(), true, true) == key:
			found = e.Name()
		}
	}
	return found, nil
}

// goneExact: nothing is stored at abs under that exact spelling. On a folding
// disk a stat of "D" finds "d"; after a case-only rename D → d the source is
// gone although the stat says otherwise (S038).
func goneExact(abs string) bool {
	if _, err := os.Lstat(abs); err != nil {
		return os.IsNotExist(err)
	}
	got, err := storedName(abs)
	return err == nil && got != filepath.Base(abs)
}

// sameEntry: from and to name one entry on disk — two spellings of one name
// on a folding disk: names equal under the fold, in one directory, one entry.
// Two names that are not fold-equal never are (a hard-linked file's two names
// are two entries; round 2's check).
func sameEntry(from, to string) bool {
	if names.FoldKey(filepath.Base(from), true, true) != names.FoldKey(filepath.Base(to), true, true) {
		return false
	}
	same := func(x, y string) bool {
		a, err := os.Lstat(x)
		if err != nil {
			return false
		}
		b, err := os.Lstat(y)
		return err == nil && os.SameFile(a, b)
	}
	return same(filepath.Dir(from), filepath.Dir(to)) && same(from, to)
}

// caseTemp is the temporary name of a case-only rename that goes in two steps:
// visible to the scan, so one left behind (recovery could not finish it) is
// met as a folder or file here, never read as a deletion.
func caseTemp(to string) string { return to + ".synckeeper-case" }

// caseRenameInPlace is the one-step rename of a case-only rename; a test
// replaces it to stand for a disk where that rename changes nothing.
var caseRenameInPlace = os.Rename

// guardedCaseRename renames from to to, two spellings of one entry on a disk
// that folds case or accents (S038): the destination's "occupant" is the
// source itself, which the occupant rule would refuse. Refused unless both
// still name one entry. macOS renames in place; a case-folding Linux
// directory (ext4, f2fs, tmpfs casefold) takes a rename onto the same entry as
// a no-op, so when the spelling did not change it goes through caseTemp —
// a run cut between the two steps is finished by recovery (finishCaseRename).
func guardedCaseRename(from, to, what string) error {
	if !sameEntry(from, to) {
		return fmt.Errorf("%s: no longer the entry being renamed; leaving it alone, replanning next cycle", what)
	}
	if err := caseRenameInPlace(from, to); err != nil {
		return err
	}
	got, err := storedName(to)
	if err != nil {
		return err
	}
	if got != filepath.Base(to) {
		tmp := caseTemp(to)
		if _, err := os.Lstat(tmp); !os.IsNotExist(err) {
			return fmt.Errorf("%s: temporary name %s is taken; leaving both alone, replanning next cycle", what, tmp)
		}
		if err := os.Rename(from, tmp); err != nil {
			return err
		}
		if err := os.Rename(tmp, to); err != nil {
			return err
		}
	}
	fsyncDir(filepath.Dir(to))
	return nil
}

// finishCaseRename completes a two-step case-only rename cut between its
// steps (S038): the entry sits at caseTemp(to) and nothing at to. Reports
// whether it renamed anything.
func finishCaseRename(to string, isDir bool) (bool, error) {
	tmp := caseTemp(to)
	info, err := os.Lstat(tmp)
	if err != nil || info.IsDir() != isDir || !isDir && !info.Mode().IsRegular() {
		return false, nil
	}
	if _, err := os.Lstat(to); !os.IsNotExist(err) {
		return false, nil
	}
	if err := os.Rename(tmp, to); err != nil {
		return false, err
	}
	fsyncDir(filepath.Dir(to))
	return true, nil
}

// fixSpelling makes the entry at abs stored under abs's own spelling when the
// disk holds it under another one equal under the fold (S038): a folder move
// replayed as a commit whose disk half left the folder under another case.
func fixSpelling(abs, what string) error {
	got, err := storedName(abs)
	if err != nil || got == "" || got == filepath.Base(abs) {
		return err
	}
	return guardedCaseRename(filepath.Join(filepath.Dir(abs), got), abs, what)
}

// guardedRemoveEmptyDir removes a directory that must already be empty —
// os.Remove's refusal of a non-empty directory is the guard: an unexpected
// survivor means the plan is wrong, and we fail rather than lose data.
func guardedRemoveEmptyDir(abs string) error {
	return os.Remove(abs)
}

// sweepInvisibleLeftovers moves entries of dir that are ignored-or-temp —
// the only content invisible to the plan by design — into dest, names
// preserved, so a quarantined directory carries its invisible leftovers
// with it instead of wedging on "directory not empty" forever (spec §3
// invariant 3, R20). Everything else is left in place for the caller's
// empty-dir removal to refuse: an unexpected survivor still means the plan
// is wrong. A rename across volumes fails the action and replans — the
// quarantine normally shares the volume, and a leftover may be a subtree.
func sweepInvisibleLeftovers(dir, dest string, ignore []string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if !names.Ignored(name, ignore) && !strings.HasPrefix(name, names.TempPrefix) {
			continue
		}
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(dir, name), filepath.Join(dest, name)); err != nil {
			return err
		}
	}
	return nil
}

// guardedMoveFile verifies the source against exp, then moves it, falling
// back to copy+remove across filesystems (the quarantine dir may live on a
// different volume than the sync dir). The destination must be free: a
// rescue copy is never overwritten (invariant 3, R21) — the caller picks a
// fresh name and the cycle replans on a collision.
func guardedMoveFile(from, to, what string, exp expectation) error {
	if _, err := guardedStat(from, what, exp, refuseVanished); err != nil {
		return err
	}
	if _, err := os.Lstat(to); err == nil {
		return fmt.Errorf("%s: destination %s is already occupied (an earlier rescue copy?); refusing to overwrite", what, to)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(from, to); err == nil {
		return nil
	}
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	if err := dst.Sync(); err != nil {
		dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	return os.Remove(from)
}

// errTrashUnusable marks a failure of the system trash itself — no trash on
// this platform, a destination we cannot write, a rename the OS refused. The
// caller falls back to the quarantine on it (invariant 3: a remote deletion
// is never a permanent local one). It is deliberately NOT returned for a
// guard refusal: content that drifted since the scan must fail the action,
// never quietly take the other road.
var errTrashUnusable = errors.New("system trash unusable")

// errSubtreeUnexpected means a directory holds something its collapsed
// delete never reasoned about (a file created after the scan, a survivor the
// plan skipped) or something that changed since the scan. The caller falls
// back to deleting the subtree item by item, so nothing the plan never saw
// is moved anywhere.
var errSubtreeUnexpected = errors.New("directory content changed since the scan")

// guardedTrashPath verifies the path against exp and hands it to the system
// trash. exp is checked first and its refusal is returned bare: an edit that
// landed after the scan wins the cycle (§4.2 "edit beats delete", R13), and
// it must not be rescued to the quarantine instead.
func guardedTrashPath(abs, what string, exp expectation, t trash.Trasher) error {
	if exp.exists {
		if _, err := guardedStat(abs, what, exp, refuseVanished); err != nil {
			return err
		}
	}
	if err := t.MoveToTrash(abs); err != nil {
		return fmt.Errorf("%w: %v", errTrashUnusable, err)
	}
	return nil
}

// guardedTrashDir moves a whole directory to the system trash after proving
// it holds exactly what the collapsed delete says it holds: every covered
// entry still matching the stat the scan pinned, and nothing else except the
// content invisible to the plan by design (ignored and temp files, which ride
// along inside the directory — R20's guarantee, now via the bin).
//
// covered is keyed by path relative to abs. An empty map is the ordinary
// "this directory should be empty" case and is checked by the same walk.
func guardedTrashDir(abs string, covered map[string]reconcile.SubtreeEntry, ignore []string, t trash.Trasher) error {
	if err := verifySubtree(abs, covered, ignore); err != nil {
		return err
	}
	if err := t.MoveToTrash(abs); err != nil {
		return fmt.Errorf("%w: %v", errTrashUnusable, err)
	}
	return nil
}

// verifySubtree walks dir and checks every visible entry against covered.
// Ignored and temp names are skipped whole (an ignored directory is not
// descended into), matching what the scanner never reported and the plan
// therefore never reasoned about.
func verifySubtree(dir string, covered map[string]reconcile.SubtreeEntry, ignore []string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		name := d.Name()
		if names.Ignored(name, ignore) || strings.HasPrefix(name, names.TempPrefix) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		e, ok := covered[rel]
		if !ok {
			return fmt.Errorf("%w: %s was not part of the planned deletion", errSubtreeUnexpected, rel)
		}
		if e.IsDir != d.IsDir() {
			return fmt.Errorf("%w: %s changed type since the scan", errSubtreeUnexpected, rel)
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if os.IsNotExist(err) {
				return nil // vanished on its own; the deletion still gets its way
			}
			return err
		}
		if !info.Mode().IsRegular() || info.Size() != e.Size || info.ModTime().UnixNano() != e.MtimeNS {
			return fmt.Errorf("%w: %s (size %d mtime %d, scanned %d/%d)",
				errSubtreeUnexpected, rel, info.Size(), info.ModTime().UnixNano(), e.Size, e.MtimeNS)
		}
		return nil
	})
}

// removeOwnTemp deletes one of synckeeper's own temp files and refuses any
// other name — the only unconditional delete the gate exposes.
func removeOwnTemp(abs string) error {
	if !strings.HasPrefix(filepath.Base(abs), names.TempPrefix) {
		return fmt.Errorf("refusing to remove %s: not a synckeeper temp file", abs)
	}
	return os.Remove(abs)
}

// localMutations are the action types that write the local tree, or (Record)
// restate its truth in the baseline — every one is checked by
// refuseLinkedPaths before it runs. TestGateClassifiesEveryActionType makes
// a new action type pick a side.
var localMutations = map[reconcile.Type]bool{
	reconcile.MkdirLocal:      true,
	reconcile.MkdirRemote:     true, // also creates the local folder
	reconcile.MoveLocal:       true,
	reconcile.ConflictBackup:  true,
	reconcile.Download:        true,
	reconcile.Record:          true,
	reconcile.QuarantineLocal: true,
}

// refuseLinkedPaths is the structural net under the planner's holds (S013,
// spec §7): a local mutation is refused when any path component between the
// sync root and its target — the target included — is a symlink. Without it
// one planner gap writes through a link into a folder outside the sync dir:
// MkdirAll and the download's temp file follow an intermediate symlink, and
// guardedStat looks at the last element only. The root itself is exempt:
// S007 resolves it, and SyncDir is the resolved path. A collapsed folder
// delete has its covered entries checked too, since its fallback removes them
// one by one. Cost: one Lstat per existing path component (each directory
// once per action), before the action's own I/O.
func (x *Executor) refuseLinkedPaths(a reconcile.Action) error {
	if !localMutations[a.Type] {
		return nil
	}
	seen := map[string]bool{}
	check := func(rel string) error {
		if rel == "" {
			return nil
		}
		p, sofar := x.SyncDir, ""
		for _, seg := range strings.Split(rel, "/") {
			p = filepath.Join(p, seg)
			sofar = path.Join(sofar, seg)
			if seen[sofar] {
				continue
			}
			seen[sofar] = true
			info, err := os.Lstat(p)
			if os.IsNotExist(err) {
				return nil // nothing below a missing component can be a link
			}
			if err != nil {
				return err
			}
			if info.Mode()&fs.ModeSymlink != 0 {
				return fmt.Errorf("%s: %s is a symlink; refusing to write through it or over it, replanning next cycle", rel, sofar)
			}
		}
		return nil
	}
	if err := check(a.RelPath); err != nil {
		return err
	}
	if err := check(a.NewRelPath); err != nil {
		return err
	}
	for _, e := range a.Subtree {
		if err := check(e.RelPath); err != nil {
			return err
		}
	}
	return nil
}
