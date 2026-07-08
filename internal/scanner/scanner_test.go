package scanner

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

func write(t *testing.T, root, rel, content string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestScanBasics(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", "hello")
	write(t, root, "sub/b.txt", "world!")
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	snap, skips, err := Scan(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(skips) != 0 {
		t.Errorf("unexpected skips: %+v", skips)
	}
	if len(snap) != 4 { // a.txt, sub, sub/b.txt, empty
		t.Fatalf("snapshot has %d entries: %+v", len(snap), snap)
	}
	if !snap["sub"].IsDir || !snap["empty"].IsDir {
		t.Error("dirs not marked as dirs")
	}
	// md5("hello") well-known value
	if snap["a.txt"].MD5 != "5d41402abc4b2a76b9719d911017c592" {
		t.Errorf("a.txt md5 = %q", snap["a.txt"].MD5)
	}
}

func TestScanTrustsBaselineHash(t *testing.T) {
	root := t.TempDir()
	p := write(t, root, "a.txt", "hello")
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]reconcile.BaseItem{
		"a.txt": {Size: info.Size(), MtimeNS: info.ModTime().UnixNano(), MD5: "trusted-fake-md5"},
	}
	snap, _, err := Scan(root, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if snap["a.txt"].MD5 != "trusted-fake-md5" {
		t.Errorf("md5 = %q, want the trusted baseline hash (no rehash)", snap["a.txt"].MD5)
	}

	// Change mtime -> must rehash.
	past := time.Now().Add(-time.Hour)
	os.Chtimes(p, past, past)
	snap, _, err = Scan(root, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if snap["a.txt"].MD5 != "5d41402abc4b2a76b9719d911017c592" {
		t.Errorf("md5 after mtime change = %q, want real hash", snap["a.txt"].MD5)
	}
}

func TestScanIgnores(t *testing.T) {
	root := t.TempDir()
	write(t, root, "keep.txt", "x")
	write(t, root, "drop.tmp", "x")
	write(t, root, ".synckeeper.tmp.123", "leftover")
	write(t, root, "node_modules/dep.js", "x")

	snap, _, err := Scan(root, nil, []string{"*.tmp", "node_modules"})
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"drop.tmp", ".synckeeper.tmp.123", "node_modules", "node_modules/dep.js"} {
		if _, ok := snap[banned]; ok {
			t.Errorf("%s should be ignored", banned)
		}
	}
	if _, ok := snap["keep.txt"]; !ok {
		t.Error("keep.txt missing")
	}
}

func TestScanSkipsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	root := t.TempDir()
	write(t, root, "real.txt", "content")
	if err := os.Symlink(filepath.Join(root, "real.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	snap, skips, err := Scan(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap["link.txt"]; ok {
		t.Error("symlink ended up in snapshot")
	}
	found := false
	for _, s := range skips {
		if s.RelPath == "link.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("symlink not reported in skips: %+v", skips)
	}
}
