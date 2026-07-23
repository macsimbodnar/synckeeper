package watch

import (
	"os"
	"testing"
)

// The watch suite targets the fsnotify backend and its seams (newNotifyWatcher,
// R15; the reload race, R14). On darwin+cgo the package init selects FSEvents as
// the production default (W3.2), so pin the backend back to fsnotify for the
// whole suite; the FSEvents backend has its own darwin+cgo test that constructs
// it directly.
func TestMain(m *testing.M) {
	newBackend = newFSNotifyBackend
	os.Exit(m.Run())
}
