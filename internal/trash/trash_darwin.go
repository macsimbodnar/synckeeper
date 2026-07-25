//go:build darwin && cgo

package trash

// macOS: Finder's own trash API. NSFileManager -trashItemAtURL: puts the item
// where the user expects it, records the "Put Back" origin, and handles
// per-volume .Trashes itself — none of which we could reproduce correctly by
// moving files around by hand.
//
// Second cgo file in the repo (after the FSEvents backend), same shape: a
// pure-Go build drops it and falls back to trash_other.go, which reports the
// trash unavailable so the executor uses the quarantine.

/*
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>

// moveToTrash returns 1 on success; on failure it returns 0 and stores a
// malloc'd message in *errMsg for the caller to free.
int synckeeperMoveToTrash(const char *path, char **errMsg);
*/
import "C"

import (
	"errors"
	"os"
	"unsafe"
)

func moveToTrash(path string) error {
	if _, err := os.Lstat(path); err != nil {
		return err // nothing to trash; the caller's expectation was wrong
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var cErr *C.char
	if C.synckeeperMoveToTrash(cPath, &cErr) == 1 {
		return nil
	}
	msg := "unknown error"
	if cErr != nil {
		msg = C.GoString(cErr)
		C.free(unsafe.Pointer(cErr))
	}
	return errors.New("move to trash: " + msg)
}

func available() bool { return true }

func describe() string { return "macOS Trash (Finder)" }
