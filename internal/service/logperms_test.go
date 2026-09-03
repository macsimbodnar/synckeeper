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

// TestLogPathLinux pins the Linux log location the hardening acts on: the XDG
// state dir, defaulting to ~/.local/state (W7-L2).
func TestLogPathLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("linux-only log file; GOOS=%s returns %q", runtime.GOOS, LogPath())
	}
	t.Setenv("XDG_STATE_HOME", "/var/tmp/state")
	if got, want := LogPath(), filepath.Join("/var/tmp/state", "synckeeper", "synckeeper.log"); got != want {
		t.Errorf("LogPath() = %q, want %q", got, want)
	}

	t.Setenv("XDG_STATE_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := LogPath(), filepath.Join(home, ".local", "state", "synckeeper", "synckeeper.log"); got != want {
		t.Errorf("LogPath() unset XDG_STATE_HOME = %q, want %q", got, want)
	}
}

// TestLinuxLogPath covers the resolver itself, so the XDG rules have coverage
// on every platform the suite runs on — LogPath only calls it on linux.
func TestLinuxLogPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only path rules")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	fallback := filepath.Join(home, ".local", "state", "synckeeper", "synckeeper.log")

	for _, tc := range []struct {
		name string
		xdg  string
		want string
	}{
		{"absolute XDG_STATE_HOME is used", "/var/tmp/state", filepath.Join("/var/tmp/state", "synckeeper", "synckeeper.log")},
		{"unset falls back to ~/.local/state", "", fallback},
		// The XDG basedir spec: a relative path is invalid and must be ignored.
		{"relative XDG_STATE_HOME is invalid, treated as unset", "state", fallback},
		{"dot-relative XDG_STATE_HOME is invalid too", "./state", fallback},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", tc.xdg)
			got, err := linuxLogPath()
			if err != nil {
				t.Fatalf("linuxLogPath: %v", err)
			}
			if got != tc.want {
				t.Errorf("linuxLogPath() = %q, want %q", got, tc.want)
			}
			if !filepath.IsAbs(got) {
				t.Errorf("linuxLogPath() = %q, want an absolute path", got)
			}
		})
	}
}
