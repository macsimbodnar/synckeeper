package watch

// The file-watching backend, extracted behind an interface (W3): the fsnotify
// implementation lives here today; an FSEvents backend (cgo, macOS) slots in
// later without touching the sync loop. Correctness never depends on any event
// arriving (spec §8.1 — events are wake-ups only; the poll tick covers
// everything a backend misses, drops, or coalesces), so a backend is free to
// fail at creation (→ pure polling) or under fd / kernel-watch-slot pressure
// (→ the failure latch).

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"github.com/fsnotify/fsnotify"

	"github.com/macsimbodnar/synckeeper/internal/names"
)

// fsWatcher is a running file-watching backend. It wakes the sync loop on
// relevant local changes and holds whatever OS resources watching needs.
type fsWatcher interface {
	// refresh makes the watch set cover root and returns how many directories
	// could not be watched (fd or kernel watch-slot pressure → the failure
	// latch; a backend reports which through backendHooks). A whole-tree
	// backend (FSEvents) watches root in one call and returns 0.
	refresh(root string) int
	// close releases every descriptor/stream the backend holds; its event
	// pump stops.
	close() error
	// needsRebuild reports whether the sync loop should periodically tear the
	// backend down and recreate it to bound a descriptor leak (W3.4). Only a
	// backend that owns a descriptor per watched *file* has anything to leak:
	// fsnotify's kqueue does (darwin/BSD), fsnotify's inotify does not (Linux),
	// and FSEvents holds none at all and is left running.
	needsRebuild() bool

	// name identifies the backend for reporting ("fsnotify", "fsevents"). It is
	// display-only: nothing in the loop branches on it (W15-U5).
	name() string
}

// backendHooks is everything a running backend reports back to the sync loop.
// Every hook is optional — a nil one is a no-op — so a backend never has to
// check before calling, and a test can supply only the hook it asserts on.
type backendHooks struct {
	// wake asks for a sync cycle: something under the root may have changed.
	// It is only ever a hint (spec §8.1); the cycle's full scan is the truth.
	wake func()
	// overflow: the kernel's event queue overflowed and events were dropped
	// (inotify's IN_Q_OVERFLOW; fsnotify raises the same error for the
	// ReadDirectoryChangesW buffer). Reporting only — a wake is what recovers.
	overflow func()
	// watchLimit: a watch registration failed for want of kernel watch slots
	// rather than for want of file descriptors — on Linux the per-user
	// fs.inotify.max_user_watches budget (S001-L4a).
	watchLimit func()
}

func (h backendHooks) noteWake() {
	if h.wake != nil {
		h.wake()
	}
}

func (h backendHooks) noteOverflow() {
	if h.overflow != nil {
		h.overflow()
	}
}

func (h backendHooks) noteWatchLimit() {
	if h.watchLimit != nil {
		h.watchLimit()
	}
}

// newNotifyWatcher creates the raw fsnotify watcher — a test seam so R15 can
// inject the fd-pressure creation failure without exhausting real descriptors.
// Tests swap it only while no watcher goroutine is running.
var newNotifyWatcher = fsnotify.NewWatcher

// newBackend is the sole place a concrete backend is chosen (and a seam for
// tests). It creates the backend over root, performs the initial watch, and
// starts the event pump bound to ctx; `hooks.wake` fires on every relevant
// local change and `ignore` supplies the current ignore globs. Returns the
// count of directories that could not be watched (fd or watch pressure → the
// latch).
var newBackend = newFSNotifyBackend

// fsnotifyBackend watches one directory at a time, refreshed by re-walking the
// tree. Its pump translates fsnotify events into wake-ups and registers
// newly-created directories immediately — files can land in them before the
// post-sync refresh.
type fsnotifyBackend struct {
	fw     *fsnotify.Watcher
	ignore func() []string
	hooks  backendHooks
}

func newFSNotifyBackend(ctx context.Context, root string, ignore func() []string, hooks backendHooks) (fsWatcher, int, error) {
	fw, err := newNotifyWatcher()
	if err != nil {
		return nil, 0, err
	}
	b := &fsnotifyBackend{fw: fw, ignore: ignore, hooks: hooks}
	failed := b.refresh(root)
	go b.pump(ctx)
	return b, failed, nil
}

func (b *fsnotifyBackend) close() error { return b.fw.Close() }

// needsRebuild is per platform, not per backend name (S001-L3): "fsnotify" is a
// different kernel API on each OS, and only some of them own a descriptor per
// watched file.
func (b *fsnotifyBackend) needsRebuild() bool { return rebuildNeededOn(runtime.GOOS) }

// rebuildNeededOn answers it for one GOOS. darwin/BSD: fsnotify is kqueue, which
// holds one fd per watched *file* and leaks it when the file is deleted faster
// than its event is processed — closing the watcher is the only way to get those
// fds back (W3.4). Linux: fsnotify is inotify, one fd per *instance* plus one
// watch descriptor per *directory*, so nothing accumulates per file and the
// teardown would buy nothing while costing a blind window each time.
//
// Windows (ReadDirectoryChangesW, one handle per directory) stays on the
// conservative arm until the Windows port measures it (S006).
func rebuildNeededOn(goos string) bool {
	switch goos {
	case "linux":
		return false
	default:
		return true
	}
}

func (b *fsnotifyBackend) name() string { return "fsnotify" }

// refresh makes the fsnotify watch set match the current directory tree and
// returns how many directories could not be watched. fsnotify drops watches
// for deleted dirs on its own; stale Adds are harmless, so a re-walk suffices.
func (b *fsnotifyBackend) refresh(root string) int {
	failed := 0
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if names.Ignored(d.Name(), b.ignore()) && p != root {
			return filepath.SkipDir
		}
		if err := b.fw.Add(p); err != nil {
			failed++
			// Why it failed decides what the latch is allowed to blame it on
			// (S001-L4a): ENOSPC is the kernel's watch budget, not this
			// process's descriptors, and only one of the two has a sysctl fix.
			if isWatchLimit(err) {
				b.hooks.noteWatchLimit()
			}
		}
		return nil
	})
	return failed
}

// pump feeds wake-ups to the sync loop until ctx is cancelled or the watcher
// closes.
func (b *fsnotifyBackend) pump(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-b.fw.Events:
			if !ok {
				return
			}
			if names.Ignored(filepath.Base(ev.Name), b.ignore()) {
				continue
			}
			if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			// New directories must be watched immediately: files may land in
			// them before the post-sync watch refresh.
			if ev.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					b.refresh(ev.Name)
				}
			}
			b.hooks.noteWake()
		case err, ok := <-b.fw.Errors:
			if !ok {
				return
			}
			b.noteError(err)
		}
	}
}

// noteError handles one error from the backend's error channel.
//
// A queue overflow is not an ordinary error: the kernel dropped events (inotify
// keeps 16384 of them, which a `git checkout` or an untar inside the sync dir
// can outrun), so real local changes are now invisible to the watcher. It is
// therefore treated as a wake — the next cycle's full scan is what finds them —
// instead of being left to the poll tick a whole interval away (S001-L4b).
// Platform-neutral on purpose: fsnotify reports the same error for Windows'
// ReadDirectoryChangesW buffer, and no correctness rests on which OS it is.
func (b *fsnotifyBackend) noteError(err error) {
	if errors.Is(err, fsnotify.ErrEventOverflow) {
		slog.Warn("file-watch event queue overflowed; events were dropped, syncing now to catch up")
		b.hooks.noteOverflow()
		b.hooks.noteWake()
		return
	}
	slog.Warn("fsnotify error", "err", err)
}
