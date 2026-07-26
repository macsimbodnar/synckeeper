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
