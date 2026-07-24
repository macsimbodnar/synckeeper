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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
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
	stream    C.FSEventStreamRef
	handle    uintptr
	root      string // symlink-resolved; event paths are filtered relative to it
	rootDev   uint64 // device id of root's volume at stream creation
	rootDevOK bool   // whether rootDev was successfully captured
	ignore    func() []string
	wake      func()
}

// fseventsRootDev returns the device id of path's filesystem. A per-volume
// FSEvents stream does not survive its volume being unmounted and remounted, and
// a remount changes the device id — so refresh compares this each cycle to
// detect a stale stream. A seam so tests can simulate a remount without a real
// mount/unmount.
var fseventsRootDev = func(path string) (uint64, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true
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
	// Resolve symlinks once (macOS /tmp and /var live under /private) and
	// register the stream on the resolved path, so the callback's paths and
	// b.root agree and the per-component ignore filter can relate them.
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	b := &fseventsBackend{root: root, ignore: ignore, wake: wake}
	b.rootDev, b.rootDevOK = fseventsRootDev(root)

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

// refresh normally does nothing: the recursive stream already covers root and
// every directory created under it, with zero per-file descriptors. Its one job
// is liveness — a per-volume FSEvents stream dies when its volume is unmounted
// and remounted (a sync dir on an external drive), and unlike fsnotify (which
// re-walks and re-adds every cycle) this stream would otherwise sit dead while
// the daemon still reports "watching". So refresh re-stats root and returns 1
// (unwatchable) when the device id changed (remount) or root vanished (unmount
// race); the loop's failure latch then degrades to polling and the recovery
// path recreates the stream on the current volume. On an unchanged volume — the
// normal case, and every internal-drive sync dir — it returns 0.
func (b *fseventsBackend) refresh(root string) int {
	dev, ok := fseventsRootDev(root)
	if !ok {
		// Root not stattable — e.g. its volume just unmounted. The engine's
		// sync-dir guard usually catches this first (hard error → backoff); this
		// covers the race and reports the stream unwatchable so the latch trips.
		return 1
	}
	if !b.rootDevOK {
		b.rootDev, b.rootDevOK = dev, true // establish a baseline missed at creation
		return 0
	}
	if dev != b.rootDev {
		return 1 // volume remounted under the stream: it is stale, force a recreate
	}
	return 0
}

// needsRebuild is false: a directory-tree stream holds no per-file descriptors,
// so there is nothing to leak and no reason to tear it down and recreate it
// (W3.4).
func (b *fseventsBackend) needsRebuild() bool { return false }

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
	if shouldWake(b.root, changed, b.ignore()) {
		b.wake()
	}
}

// shouldWake reports whether any changed path is worth a sync cycle. Finder
// rewrites .DS_Store constantly and every wake is a full scan, so ignored
// paths are filtered out — and a path under an ignored *directory* counts as
// ignored too (every component under root is matched, not just the basename):
// the scanner skips those subtrees entirely, and the fsnotify backend never
// even watches inside them, so waking on their churn would buy a full rescan
// for nothing (W3 adversarial check, 2026-07-23). An empty batch never wakes.
func shouldWake(root string, changed, ignore []string) bool {
	for _, p := range changed {
		if !ignoredPath(root, p, ignore) {
			return true
		}
	}
	return false
}

// ignoredPath reports whether p is ignored relative to root: any path
// component of p under root matching the ignore globs ignores the whole path.
// The root itself is never ignored (a MustScanSubDirs batch can hand it
// back), and a path that doesn't resolve under root keeps the old
// basename-only filter as the safe fallback.
func ignoredPath(root, p string, ignore []string) bool {
	rel, err := filepath.Rel(root, p)
	switch {
	case err == nil && rel == ".":
		return false // the root itself: always worth a wake
	case err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)):
		return names.Ignored(filepath.Base(p), ignore)
	}
	for _, seg := range strings.Split(rel, string(filepath.Separator)) {
		if names.Ignored(seg, ignore) {
			return true
		}
	}
	return false
}
