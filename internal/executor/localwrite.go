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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/macsimbodnar/synckeeper/internal/names"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
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

// guardedMoveFile verifies the source against exp, then moves it, falling
// back to copy+remove across filesystems (the quarantine dir may live on a
// different volume than the sync dir).
func guardedMoveFile(from, to, what string, exp expectation) error {
	if _, err := guardedStat(from, what, exp, refuseVanished); err != nil {
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

// removeOwnTemp deletes one of synckeeper's own temp files and refuses any
// other name — the only unconditional delete the gate exposes.
func removeOwnTemp(abs string) error {
	if !strings.HasPrefix(filepath.Base(abs), names.TempPrefix) {
		return fmt.Errorf("refusing to remove %s: not a synckeeper temp file", abs)
	}
	return os.Remove(abs)
}
