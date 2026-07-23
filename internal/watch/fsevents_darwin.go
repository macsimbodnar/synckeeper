//go:build darwin && cgo

package watch

// The macOS FSEvents backend (W3.2) — the repo's first cgo. One recursive
// directory-tree stream covers the whole sync tree with zero per-file
// descriptors (spec §10): new directories are picked up automatically, so
// refresh is a no-op and the fd-pressure count is always zero. Correctness
// never depends on it (spec §8.1) — the poll tick and, when cgo is disabled,
// the pure-Go fsnotify fallback cover any gap.

/*
#cgo LDFLAGS: -framework CoreServices
#include <CoreServices/CoreServices.h>
#include <stdlib.h>

extern void goFSEventsCallback(uintptr_t info, size_t n, char **paths);

static void synckeeper_fsevents_cb(ConstFSEventStreamRef stream, void *info,
        size_t n, void *paths, const FSEventStreamEventFlags flags[],
        const FSEventStreamEventId ids[]) {
    goFSEventsCallback((uintptr_t)info, n, (char **)paths);
}

// synckeeper_fsevents_start creates a recursive, file-level FSEvents stream
// over path and delivers callbacks on a private serial dispatch queue.
// Returns NULL on failure.
static FSEventStreamRef synckeeper_fsevents_start(uintptr_t info, const char *path, double latency) {
    CFStringRef cfp = CFStringCreateWithCString(NULL, path, kCFStringEncodingUTF8);
    if (cfp == NULL) return NULL;
    CFArrayRef paths = CFArrayCreate(NULL, (const void **)&cfp, 1, &kCFTypeArrayCallBacks);
    CFRelease(cfp);
    if (paths == NULL) return NULL;

    FSEventStreamContext ctx = {0, (void *)info, NULL, NULL, NULL};
    FSEventStreamRef stream = FSEventStreamCreate(NULL, &synckeeper_fsevents_cb, &ctx, paths,
        kFSEventStreamEventIdSinceNow, latency,
        kFSEventStreamCreateFlagFileEvents | kFSEventStreamCreateFlagNoDefer);
    CFRelease(paths);
    if (stream == NULL) return NULL;

    dispatch_queue_t q = dispatch_queue_create("com.synckeeper.fsevents", NULL);
    FSEventStreamSetDispatchQueue(stream, q);
    dispatch_release(q); // the stream retains the queue
    if (!FSEventStreamStart(stream)) {
        FSEventStreamInvalidate(stream);
        FSEventStreamRelease(stream);
        return NULL;
    }
    return stream;
}

static void synckeeper_fsevents_stop(FSEventStreamRef stream) {
    FSEventStreamStop(stream);
    FSEventStreamInvalidate(stream); // after this returns the callback won't fire again
    FSEventStreamRelease(stream);
}
*/
import "C"

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"unsafe"

	"github.com/macsimbodnar/synckeeper/internal/names"
)

func init() {
	// On macOS with cgo, FSEvents is the native backend. The pure-Go fsnotify
	// backend stays the fallback when cgo is disabled (this file drops out) and
	// on every other platform. Tests pin the backend explicitly (TestMain).
	newBackend = newFSEventsBackend
}

// fseventsBackend implements fsWatcher over the macOS FSEvents API.
type fseventsBackend struct {
	stream C.FSEventStreamRef
	handle uintptr
	ignore func() []string
	wake   func()
}

// The FSEvents callback fires on a dispatch-queue thread and reaches Go by an
// integer handle (a Go pointer cannot be stashed in the C stream context). The
// registry maps the handle back to the backend.
var (
	fseventsMu       sync.Mutex
	fseventsRegistry = map[uintptr]*fseventsBackend{}
	fseventsNextID   uintptr
)

// newFSEventsBackend starts a recursive FSEvents stream over root. ctx is
// unused: FSEvents delivers on its own dispatch queue, and the sync loop always
// calls close() (deferred at shutdown, and on each rebuild), which stops it.
func newFSEventsBackend(_ context.Context, root string, ignore func() []string, wake func()) (fsWatcher, int, error) {
	b := &fseventsBackend{ignore: ignore, wake: wake}

	fseventsMu.Lock()
	fseventsNextID++
	b.handle = fseventsNextID
	fseventsRegistry[b.handle] = b
	fseventsMu.Unlock()

	cpath := C.CString(root)
	defer C.free(unsafe.Pointer(cpath))
	// 0.1 s FSEvents-side latency; the sync loop's debounce coalesces further.
	stream := C.synckeeper_fsevents_start(C.uintptr_t(b.handle), cpath, C.double(0.1))
	if stream == nil {
		fseventsMu.Lock()
		delete(fseventsRegistry, b.handle)
		fseventsMu.Unlock()
		return nil, 0, errors.New("fsevents: could not create or start the event stream")
	}
	b.stream = stream
	return b, 0, nil
}

// refresh is a no-op: the recursive stream already covers root and every
// directory created under it, so nothing can fail to be watched.
func (b *fseventsBackend) refresh(string) int { return 0 }

func (b *fseventsBackend) close() error {
	// Stop first (Invalidate blocks until in-flight callbacks finish and
	// guarantees no further ones), then drop the handle — so a callback can
	// never find a half-torn-down backend.
	C.synckeeper_fsevents_stop(b.stream)
	fseventsMu.Lock()
	delete(fseventsRegistry, b.handle)
	fseventsMu.Unlock()
	return nil
}

//export goFSEventsCallback
func goFSEventsCallback(info C.uintptr_t, n C.size_t, paths **C.char) {
	fseventsMu.Lock()
	b := fseventsRegistry[uintptr(info)]
	fseventsMu.Unlock()
	if b == nil {
		return // closed between the OS scheduling this callback and now
	}
	cpaths := unsafe.Slice(paths, int(n))
	changed := make([]string, len(cpaths))
	for i, cp := range cpaths {
		changed[i] = C.GoString(cp)
	}
	if shouldWake(changed, b.ignore()) {
		b.wake()
	}
}

// shouldWake reports whether any changed path is worth a sync cycle. Finder
// rewrites .DS_Store constantly and every wake is a full scan, so ignored
// paths are filtered out here — the same basename-glob ignore the fsnotify
// backend applies per event (spec §5/§8.3). An empty batch never wakes.
func shouldWake(changed, ignore []string) bool {
	for _, p := range changed {
		if !names.Ignored(filepath.Base(p), ignore) {
			return true
		}
	}
	return false
}
