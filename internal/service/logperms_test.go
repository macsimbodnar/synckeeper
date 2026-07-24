package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestRestrictToOwner covers the log-hardening helper: an existing world-
// readable file is tightened to 0600, while an empty or missing path is a
// no-op (a foreground run or a non-file-log platform must not error).
func TestRestrictToOwner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits not meaningful on Windows")
	}

	// Empty path (no file-based log) is a no-op.
	if err := restrictToOwner(""); err != nil {
		t.Fatalf("empty path: %v", err)
	}

	// Missing file is not an error (foreground `watch`, or log not created yet).
	if err := restrictToOwner(filepath.Join(t.TempDir(), "nope.log")); err != nil {
		t.Fatalf("missing file: %v", err)
	}

	// A world-readable log is tightened to owner-only.
	p := filepath.Join(t.TempDir(), "synckeeper.log")
	if err := os.WriteFile(p, []byte("rel_path secret-file.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o644); err != nil { // defeat umask so the "before" is really 0644
		t.Fatal(err)
	}
	if err := restrictToOwner(p); err != nil {
		t.Fatalf("restrict: %v", err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("perm = %o, want 600", got)
	}
}

// TestLogPathDarwin pins the macOS log location the hardening acts on.
func TestLogPathDarwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skipf("darwin-only log file; GOOS=%s returns %q", runtime.GOOS, LogPath())
	}
	if p := LogPath(); !strings.HasSuffix(p, filepath.Join("Library", "Logs", "synckeeper.log")) {
		t.Errorf("LogPath() = %q, want it to end in Library/Logs/synckeeper.log", p)
	}
}
