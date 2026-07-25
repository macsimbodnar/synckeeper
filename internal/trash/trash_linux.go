//go:build linux

package trash

// The freedesktop.org trash specification, in pure Go.
//
// A trashed item is two files: the content moved into <trash>/files/<name>,
// and <trash>/info/<name>.trashinfo recording where it came from and when —
// that pairing is what lets a file manager offer "Restore". The name is
// claimed by creating the .trashinfo with O_EXCL, which is the spec's own
// protocol for racing trashers, so two machines (or two cycles) can never
// overwrite each other's rescued content.
//
// Trashing is a rename, so it only works within one filesystem. When the sync
// dir lives on another volume the spec provides a per-volume trash at the
// mount point; we use it rather than copying bytes across devices.

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// deviceOf reports the filesystem device id of path. A test seam: the
// cross-volume branch is otherwise unreachable without a second filesystem.
var deviceOf = func(path string) (uint64, error) {
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Dev), nil
}

func moveToTrash(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(abs); err != nil {
		return err // nothing to trash; the caller's expectation was wrong
	}

	dir, relTo, err := trashDirFor(abs)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o700); err != nil {
		return fmt.Errorf("create trash files dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "info"), 0o700); err != nil {
		return fmt.Errorf("create trash info dir: %w", err)
	}

	// The recorded original path is absolute for the home trash and relative
	// to the volume root for a per-volume trash (spec: "Path").
	recorded := abs
	if relTo != "" {
		if r, err := filepath.Rel(relTo, abs); err == nil {
			recorded = r
		}
	}

	name, info, err := claimName(dir, filepath.Base(abs), recorded)
	if err != nil {
		return err
	}
	dest := filepath.Join(dir, "files", name)
	if err := os.Rename(abs, dest); err != nil {
		os.Remove(info) // release the claimed name; nothing was moved
		return fmt.Errorf("move to trash: %w", err)
	}
	return nil
}

// claimName reserves a free name in the trash by creating its .trashinfo with
// O_EXCL — the spec's atomic claim — and returns the name plus the info file's
// path so a failed move can release it.
func claimName(dir, base, recorded string) (name, infoPath string, err error) {
	for n := 1; n <= 1000; n++ {
		candidate := numbered(base, n)
		infoPath = filepath.Join(dir, "info", candidate+".trashinfo")
		f, err := os.OpenFile(infoPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if os.IsExist(err) {
			continue // another item already holds this name
		}
		if err != nil {
			return "", "", fmt.Errorf("claim trash name: %w", err)
		}
		_, werr := f.WriteString(trashInfo(recorded, time.Now()))
		cerr := f.Close()
		if werr != nil || cerr != nil {
			os.Remove(infoPath)
			if werr != nil {
				return "", "", werr
			}
			return "", "", cerr
		}
		// A stale files/<name> from an interrupted trasher would be silently
		// replaced by the rename below; refuse instead and take the next name.
		if _, serr := os.Lstat(filepath.Join(dir, "files", candidate)); serr == nil {
			os.Remove(infoPath)
			continue
		}
		return candidate, infoPath, nil
	}
	return "", "", fmt.Errorf("trash: no free name for %q", base)
}

// numbered is the collision suffix, inserted before the extension the way
// the desktop's own trasher does it ("notes.txt" -> "notes.2.txt"), so a
// browsed trash reads consistently whoever put the item there.
func numbered(base string, n int) string {
	if n <= 1 {
		return base
	}
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext) + "." + strconv.Itoa(n) + ext
}

// trashInfo renders the .trashinfo body. Path is percent-encoded per the
// spec (as a URL path, so separators stay readable); DeletionDate is local
// time in RFC 3339 without a zone, which is what the spec asks for.
func trashInfo(path string, when time.Time) string {
	escaped := (&url.URL{Path: path}).EscapedPath()
	return "[Trash Info]\nPath=" + escaped + "\nDeletionDate=" + when.Format("2006-01-02T15:04:05") + "\n"
}

// trashDirFor picks the trash directory for abs: the home trash when they
// share a filesystem, otherwise the per-volume trash at the mount point.
// relTo is the volume root the recorded path must be relative to, empty for
// the home trash.
func trashDirFor(abs string) (dir, relTo string, err error) {
	home := homeTrashDir()
	if home != "" {
		// Compare against an existing ancestor: the trash dir itself may not
		// exist yet, and a missing path has no device.
		if sameDevice(existingAncestor(home), filepath.Dir(abs)) {
			return home, "", nil
		}
	}
	mount, err := mountPointOf(abs)
	if err != nil {
		if home == "" {
			return "", "", ErrUnavailable
		}
		return home, "", nil
	}
	// The spec's preferred $topdir/.Trash/$uid is only safe when $topdir/.Trash
	// exists, is sticky and is not a symlink; otherwise $topdir/.Trash-$uid.
	uid := strconv.Itoa(os.Getuid())
	shared := filepath.Join(mount, ".Trash")
	if fi, serr := os.Lstat(shared); serr == nil &&
		fi.IsDir() && fi.Mode()&os.ModeSymlink == 0 && fi.Mode()&os.ModeSticky != 0 {
		return filepath.Join(shared, uid), mount, nil
	}
	return filepath.Join(mount, ".Trash-"+uid), mount, nil
}

// homeTrashDir is $XDG_DATA_HOME/Trash, defaulting to ~/.local/share/Trash.
func homeTrashDir() string {
	if data := os.Getenv("XDG_DATA_HOME"); data != "" && filepath.IsAbs(data) {
		return filepath.Join(data, "Trash")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "Trash")
}

// existingAncestor walks up until something exists, so a not-yet-created
// trash directory can still be device-compared through its parent.
func existingAncestor(path string) string {
	for p := path; ; {
		if _, err := os.Lstat(p); err == nil {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		p = parent
	}
}

func sameDevice(a, b string) bool {
	da, err := deviceOf(a)
	if err != nil {
		return false
	}
	db, err := deviceOf(b)
	if err != nil {
		return false
	}
	return da == db
}

// mountPointOf walks up from path until the device id changes; the last path
// on the original device is that filesystem's mount point.
func mountPointOf(path string) (string, error) {
	dev, err := deviceOf(path)
	if err != nil {
		return "", err
	}
	cur := path
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			return cur, nil // reached "/"
		}
		pdev, err := deviceOf(parent)
		if err != nil || pdev != dev {
			return cur, nil
		}
		cur = parent
	}
}

func available() bool {
	dir := homeTrashDir()
	if dir == "" {
		return false
	}
	// Writable if it exists; creatable if it does not.
	if fi, err := os.Lstat(dir); err == nil {
		return fi.IsDir()
	}
	return os.MkdirAll(dir, 0o700) == nil
}

func describe() string {
	dir := homeTrashDir()
	if dir == "" {
		return "unavailable (no home directory)"
	}
	return "freedesktop trash at " + strings.TrimSuffix(dir, "/")
}
