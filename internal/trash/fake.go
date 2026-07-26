package trash

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// Fake is a bin in a directory of the caller's choosing — the trash the test
// suite uses. It is a real implementation (items are really moved, and can be
// inspected afterwards), just not the user's own bin: no test may ever move a
// file into the developer's real trash, so both the engine and the executor
// default to a Fake under TestMain.
//
// It also plays the two failure modes the fallback path exists for: a
// platform with no trash (Unavailable) and a trash that refuses an item
// (MoveErr).
type Fake struct {
	Dir         string // where trashed items land
	Unavailable bool   // report no usable trash, as a CGO_ENABLED=0 darwin or Windows build does
	MoveErr     error  // fail every move, as a cross-device or read-only trash does

	mu    sync.Mutex
	moved []string // rel names inside Dir, in move order
}

// NewFake returns a bin that trashes into dir.
func NewFake(dir string) *Fake { return &Fake{Dir: dir} }

func (f *Fake) MoveToTrash(path string) error {
	if f.Unavailable {
		return ErrUnavailable
	}
	if f.MoveErr != nil {
		return f.MoveErr
	}
	if _, err := os.Lstat(path); err != nil {
		return err
	}
	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		return err
	}
	base := filepath.Base(path)
	name := base
	for n := 2; ; n++ {
		if _, err := os.Lstat(filepath.Join(f.Dir, name)); errors.Is(err, os.ErrNotExist) {
			break
		}
		name = base + "." + strconv.Itoa(n)
	}
	if err := os.Rename(path, filepath.Join(f.Dir, name)); err != nil {
		return err
	}
	f.mu.Lock()
	f.moved = append(f.moved, name)
	f.mu.Unlock()
	return nil
}

func (f *Fake) Available() bool { return !f.Unavailable }

func (f *Fake) Describe() string {
	if f.Unavailable {
		return "unavailable (test)"
	}
	return "test bin at " + f.Dir
}

// Moved reports the names the bin received, in order — one entry per move, so
// a test can assert that a whole folder arrived as ONE entry.
func (f *Fake) Moved() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.moved...)
}
