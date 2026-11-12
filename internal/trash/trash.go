// Package trash moves local files and directories into the operating
// system's trash — the bin the user's desktop already shows and can restore
// from — instead of Synckeeper's own quarantine directory (spec §3 invariant
// 3, §10 "trash" module; W13).
//
// It is a small per-OS module behind one interface, like fswatch and service:
// Linux implements the freedesktop.org trash specification in pure Go, macOS
// calls Finder's own API through cgo, and every other build reports the trash
// unavailable so the caller falls back to the quarantine. A deletion is never
// permanent on any path.
package trash

import "errors"

// ErrUnavailable means this build or platform has no system trash the caller
// can use. It is a sentinel so the executor falls back to the quarantine
// deliberately rather than by matching an error string.
var ErrUnavailable = errors.New("no system trash available on this platform")

// MoveToTrash moves path (a file or a whole directory) into the system trash.
// The move is a rename, so it never copies bytes: a destination on another
// filesystem is an error rather than a silent duplication, and the caller
// falls back to the quarantine.
func MoveToTrash(path string) error { return moveToTrash(path) }

// Available reports whether this platform has its default bin — on Linux the
// home trash — for `info`, `status` and `doctor` to describe. A caller about
// to delete asks AvailableFor instead: on Linux an item on another filesystem
// goes to that volume's trash, which this does not probe (S016).
func Available() bool { return available() }

// AvailableFor reports whether MoveToTrash can be expected to work for an
// item directly inside dir, so callers can pick their strategy before
// mutating anything — the mass-delete guard above all.
func AvailableFor(dir string) bool { return availableFor(dir) }

// Describe names the destination for `info` / `status` — e.g. the trash
// directory on Linux — or says why there is none.
func Describe() string { return describe() }

// Trasher is the system trash as the sync engine consumes it. The engine and
// the executor hold one instead of calling the package functions directly, so
// tests inject a fake bin and the suite never touches the developer's own
// trash.
//
// It deliberately has no dir-less Available: the bin that matters is the one
// the sync dir's items will actually reach.
type Trasher interface {
	MoveToTrash(path string) error
	AvailableFor(dir string) bool
	Describe() string
}

// OS returns the platform's trash — the Trasher every non-test caller wants.
func OS() Trasher { return osTrash{} }

type osTrash struct{}

func (osTrash) MoveToTrash(path string) error { return MoveToTrash(path) }
func (osTrash) AvailableFor(dir string) bool  { return AvailableFor(dir) }
func (osTrash) Describe() string              { return Describe() }
