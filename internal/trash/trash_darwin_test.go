//go:build darwin && cgo

package trash

import (
	"os"
	"path/filepath"
	"testing"
)

// The macOS path can only be verified by actually trashing something, which
// leaves an item in the user's Trash — so it is opt-in rather than part of
// every `make test` run:
//
//	SYNCKEEPER_TRASH_TEST=1 go test ./internal/trash/
//
// Remove the leftover "synckeeper-trash-test.txt" from the Trash afterwards.
func TestMoveToTrashDarwin(t *testing.T) {
	if os.Getenv("SYNCKEEPER_TRASH_TEST") == "" {
		t.Skip("set SYNCKEEPER_TRASH_TEST=1 to trash a real file into the user's Trash")
	}
	src := filepath.Join(t.TempDir(), "synckeeper-trash-test.txt")
	if err := os.WriteFile(src, []byte("trash me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := MoveToTrash(src); err != nil {
		t.Fatalf("MoveToTrash: %v", err)
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Error("source still exists after trashing")
	}
}

func TestMoveToTrashDarwinMissing(t *testing.T) {
	if err := MoveToTrash(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("trashing a missing path must fail")
	}
}

func TestAvailableDarwin(t *testing.T) {
	if !Available() {
		t.Error("the macOS Trash is always available")
	}
	if Describe() == "" {
		t.Error("Describe() must name the destination")
	}
}
