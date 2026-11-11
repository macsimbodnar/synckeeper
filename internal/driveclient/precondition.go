package driveclient

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// Precondition is the Drive state a mutation was planned against — the
// remote arm of spec §7's write gate (S010, audit 2026-11-11 F04). Update,
// Trash and Move given a context carrying one re-read the item immediately
// before mutating it and refuse with ErrDrift when Drive no longer holds what
// the plan saw: another machine's edit, rename, move or trash landed after
// this machine's refresh, and acting anyway would make the last writer win.
//
// No version number: Drive bumps `version` for changes the engine does not
// model, and a mirror that never learns of such a bump would refuse the same
// mutation every cycle. Every field here is one the change feed reports, so
// the expectation and Drive agree again once the feed catches up.
//
// It travels in the context, not as a parameter, so every wrapper between the
// executor and the client passes it through untouched and the check runs as
// close to the mutating request as the client can put it.
type Precondition struct {
	FileID   string // the item the mutation targets; a mismatch is a programming error
	ParentID string // the folder the plan saw it in
	Name     string
	IsDir    bool
	MD5      string // files only: a folder has no content hash
}

// ErrDrift marks a mutation refused because the item changed on Drive since
// the plan was built. The action fails and the next cycle replans.
var ErrDrift = errors.New("changed on Drive since the plan")

type preconditionKey struct{}

// WithPrecondition returns ctx carrying p for the one mutation of p.FileID it
// is passed to.
func WithPrecondition(ctx context.Context, p Precondition) context.Context {
	return context.WithValue(ctx, preconditionKey{}, p)
}

// preconditionFor returns the precondition ctx carries for a mutation of
// fileID. A precondition naming another item refuses rather than being
// skipped: the gate must never pass by accident.
func preconditionFor(ctx context.Context, fileID string) (Precondition, bool, error) {
	p, ok := ctx.Value(preconditionKey{}).(Precondition)
	if !ok {
		return Precondition{}, false, nil
	}
	if p.FileID != fileID {
		return Precondition{}, false, fmt.Errorf("drive write gate: precondition for %s passed to a mutation of %s", p.FileID, fileID)
	}
	return p, true, nil
}

// verify judges the item as one metadata read returned it. A read that failed
// for any reason other than "gone" fails the mutation too: an unchecked write
// is exactly what the gate exists to prevent, and the action retries next
// cycle like any other failure.
func (p Precondition) verify(cur File, getErr error) error {
	if getErr != nil {
		if IsNotFound(getErr) {
			return p.drift("gone from Drive")
		}
		return fmt.Errorf("drive write gate: re-reading %s before the mutation: %w", p.FileID, getErr)
	}
	switch {
	case cur.Trashed:
		return p.drift("moved to Drive's bin")
	case cur.IsDir() != p.IsDir:
		return p.drift("changed type")
	case !p.IsDir && cur.MD5 != p.MD5:
		return p.drift(fmt.Sprintf("content changed (md5 %s, planned %s)", cur.MD5, p.MD5))
	case cur.Name != p.Name:
		return p.drift(fmt.Sprintf("renamed to %q (planned %q)", cur.Name, p.Name))
	case !slices.Contains(cur.Parents, p.ParentID):
		return p.drift(fmt.Sprintf("moved to another folder (parents %v, planned %s)", cur.Parents, p.ParentID))
	}
	return nil
}

func (p Precondition) drift(what string) error {
	return fmt.Errorf("%w: %s %s; leaving it alone, replanning next cycle", ErrDrift, p.FileID, what)
}
