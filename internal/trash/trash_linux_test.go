//go:build linux

package trash

import (
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// trashHome points the home trash at a temp dir and returns it.
func trashHome(t *testing.T) string {
	t.Helper()
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	return filepath.Join(data, "Trash")
}

// readInfo returns the decoded Path= and raw DeletionDate= of a trashinfo.
func readInfo(t *testing.T, trash, name string) (path, deleted string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(trash, "info", name+".trashinfo"))
	if err != nil {
		t.Fatalf("no .trashinfo for %q: %v", name, err)
	}
	body := string(raw)
	if !strings.HasPrefix(body, "[Trash Info]\n") {
		t.Errorf("trashinfo missing its header:\n%s", body)
	}
	for _, line := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(line, "Path="):
			p, err := url.PathUnescape(strings.TrimPrefix(line, "Path="))
			if err != nil {
				t.Fatalf("Path= is not valid percent-encoding: %v", err)
			}
			path = p
		case strings.HasPrefix(line, "DeletionDate="):
			deleted = strings.TrimPrefix(line, "DeletionDate=")
		}
	}
	return path, deleted
}

// W13-T13.1: a file lands in the home trash with a restorable .trashinfo.
func TestMoveToTrashFile(t *testing.T) {
	trash := trashHome(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := MoveToTrash(src); err != nil {
		t.Fatalf("MoveToTrash: %v", err)
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Error("source still exists after trashing")
	}
	got, err := os.ReadFile(filepath.Join(trash, "files", "notes.txt"))
	if err != nil {
		t.Fatalf("content not in trash: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("trashed content = %q, want %q", got, "hello")
	}
	path, deleted := readInfo(t, trash, "notes.txt")
	if path != src {
		t.Errorf("recorded Path=%q, want the original absolute path %q", path, src)
	}
	if _, err := time.Parse("2006-01-02T15:04:05", deleted); err != nil {
		t.Errorf("DeletionDate %q is not the spec's format: %v", deleted, err)
	}
}

// A whole directory moves as one entry — the granularity the user expects
// when they delete a folder (W13-T2).
func TestMoveToTrashDirectory(t *testing.T) {
	trash := trashHome(t)
	dir := t.TempDir()
	sub := filepath.Join(dir, "pack", "Vector")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "a.svg"), []byte("svg"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := MoveToTrash(filepath.Join(dir, "pack")); err != nil {
		t.Fatalf("MoveToTrash(dir): %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "pack")); !os.IsNotExist(err) {
		t.Error("source directory still exists")
	}
	if _, err := os.Stat(filepath.Join(trash, "files", "pack", "Vector", "a.svg")); err != nil {
		t.Errorf("subtree did not travel with the directory: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(trash, "files"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("trash holds %d entries, want exactly 1 for one deleted folder", len(entries))
	}
}

// W13-T13.2: a colliding name uniquifies instead of overwriting the earlier
// rescued content — the O_EXCL claim.
func TestMoveToTrashUniquifiesOnCollision(t *testing.T) {
	trash := trashHome(t)
	for i, content := range []string{"first", "second", "third"} {
		dir := t.TempDir()
		src := filepath.Join(dir, "notes.txt")
		if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := MoveToTrash(src); err != nil {
			t.Fatalf("MoveToTrash #%d: %v", i+1, err)
		}
	}
	for name, want := range map[string]string{
		"notes.txt":   "first",
		"notes.2.txt": "second",
		"notes.3.txt": "third",
	} {
		got, err := os.ReadFile(filepath.Join(trash, "files", name))
		if err != nil {
			t.Errorf("missing %s: %v", name, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q — an earlier rescue copy was overwritten", name, got, want)
		}
		if _, err := os.Stat(filepath.Join(trash, "info", name+".trashinfo")); err != nil {
			t.Errorf("%s has no paired .trashinfo: %v", name, err)
		}
	}
}

// Names needing escaping stay restorable: Path= must decode back exactly.
func TestTrashInfoPathEncoding(t *testing.T) {
	trash := trashHome(t)
	dir := t.TempDir()
	const base = "a b#c%d\ne.txt"
	src := filepath.Join(dir, base)
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := MoveToTrash(src); err != nil {
		t.Fatalf("MoveToTrash: %v", err)
	}
	path, _ := readInfo(t, trash, base)
	if path != src {
		t.Errorf("Path= decoded to %q, want %q", path, src)
	}
	raw, err := os.ReadFile(filepath.Join(trash, "info", base+".trashinfo"))
	if err != nil {
		t.Fatal(err)
	}
	// A raw newline in the value would corrupt the key/value file.
	if body := string(raw); strings.Count(body, "\n") != 3 {
		t.Errorf("trashinfo has an unescaped newline in a value:\n%q", body)
	}
}

// W13-T13.3: when the item is on another filesystem than the home trash, the
// per-volume trash at the mount point is used and Path= is recorded relative
// to that volume root. Driven through the deviceOf seam so it runs without a
// second real filesystem.
func TestVolumeTrashOnOtherFilesystem(t *testing.T) {
	trashHome(t)
	volume := t.TempDir()
	deep := filepath.Join(volume, "sub", "dir")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(deep, "f.txt")
	if err := os.WriteFile(src, []byte("v"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Everything at or below `volume` is device 2; everything else is 1.
	orig := deviceOf
	deviceOf = func(path string) (uint64, error) {
		if path == volume || strings.HasPrefix(path, volume+string(filepath.Separator)) {
			return 2, nil
		}
		return 1, nil
	}
	defer func() { deviceOf = orig }()

	if err := MoveToTrash(src); err != nil {
		t.Fatalf("MoveToTrash: %v", err)
	}
	volTrash := filepath.Join(volume, ".Trash-"+strconv.Itoa(os.Getuid()))
	if _, err := os.Stat(filepath.Join(volTrash, "files", "f.txt")); err != nil {
		t.Fatalf("item did not land in the per-volume trash %s: %v", volTrash, err)
	}
	path, _ := readInfo(t, volTrash, "f.txt")
	if want := filepath.Join("sub", "dir", "f.txt"); path != want {
		t.Errorf("volume trash recorded Path=%q, want %q (relative to the volume root)", path, want)
	}
}

// Trashing something that is not there is an error, not a silent success:
// the caller stated an expectation and it was wrong.
func TestMoveToTrashMissing(t *testing.T) {
	trashHome(t)
	if err := MoveToTrash(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("trashing a missing path must fail")
	}
}

func TestAvailableAndDescribe(t *testing.T) {
	trash := trashHome(t)
	if !Available() {
		t.Error("home trash should be available with XDG_DATA_HOME set to a writable dir")
	}
	if got := Describe(); !strings.Contains(got, trash) {
		t.Errorf("Describe() = %q, want it to name %q", got, trash)
	}
}

// Opt-in check against the *real* desktop trash (no XDG override), so the
// freedesktop pairing is verified by the machine's own file manager rather
// than only by our reader:
//
//	SYNCKEEPER_TRASH_TEST=1 go test ./internal/trash/ -run RealDesktop
//
// It cleans up after itself, removing only the item it created.
func TestMoveToTrashRealDesktop(t *testing.T) {
	if os.Getenv("SYNCKEEPER_TRASH_TEST") == "" {
		t.Skip("set SYNCKEEPER_TRASH_TEST=1 to use the real desktop trash")
	}
	src := filepath.Join(t.TempDir(), "synckeeper-trash-check.txt")
	if err := os.WriteFile(src, []byte("synckeeper W13 real-desktop check"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := MoveToTrash(src); err != nil {
		t.Fatalf("MoveToTrash: %v", err)
	}
	trash := homeTrashDir()
	files := filepath.Join(trash, "files", "synckeeper-trash-check.txt")
	info := filepath.Join(trash, "info", "synckeeper-trash-check.txt.trashinfo")
	t.Cleanup(func() { os.Remove(files); os.Remove(info) })

	if _, err := os.Stat(files); err != nil {
		t.Fatalf("not in the real trash: %v", err)
	}
	raw, err := os.ReadFile(info)
	if err != nil {
		t.Fatalf("no .trashinfo: %v", err)
	}
	t.Logf("trash dir: %s\n%s", trash, raw)
}
