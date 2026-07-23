package watch

import (
	"os"
	"testing"
)

// productionBackend is the platform default selected at package init — FSEvents
// on darwin+cgo (W3.2), fsnotify elsewhere — captured before TestMain pins the
// suite to fsnotify. TestSoak restores it so the soak validates whatever the
// production build actually runs (W3.5).
var productionBackend = newFSNotifyBackend

// The watch suite targets the fsnotify backend and its seams (newNotifyWatcher,
// R15; the reload race, R14). On darwin+cgo the package init selects FSEvents as
// the production default (W3.2), so pin the backend back to fsnotify for the
// whole suite; the FSEvents backend has its own darwin+cgo test that constructs
// it directly.
func TestMain(m *testing.M) {
	productionBackend = newBackend
	newBackend = newFSNotifyBackend
	os.Exit(m.Run())
}
