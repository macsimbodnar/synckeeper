package driveclient

import (
	"errors"
	"net/http"
	"strings"

	"google.golang.org/api/googleapi"
)

// ErrNotFound marks a Drive item that does not exist (or that this account can
// no longer see at all). The fake returns it; the real client maps Drive's 404
// onto it. It is a sentinel because two W18 paths branch on "gone" versus
// "something went wrong", and those must never be confused:
//
//   - root.Resolve treats a NotFound root as "recreate and re-upload", so a
//     transient failure misread as NotFound would re-upload the whole tree.
//   - seedOrphanCreates treats a NotFound parent as "nothing left to
//     duplicate", so the reverse mistake would resurrect the W17 duplicate.
//
// Deliberately narrow: only 404. A 403 is a permission or quota problem, which
// is usually transient and must fail loudly rather than look like deletion.
var ErrNotFound = errors.New("drive: item not found")

// IsNotFound reports whether err means the item is gone from Drive.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNotFound) {
		return true
	}
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == http.StatusNotFound
	}
	return false
}

// notFoundf wraps a message as an ErrNotFound, for the fake.
func notFoundf(what, id string) error {
	return &notFoundErr{what: what, id: id}
}

type notFoundErr struct{ what, id string }

func (e *notFoundErr) Error() string {
	var b strings.Builder
	b.WriteString("fake drive: no ")
	b.WriteString(e.what)
	b.WriteString(" ")
	b.WriteString(e.id)
	return b.String()
}

func (e *notFoundErr) Is(target error) bool { return target == ErrNotFound }
