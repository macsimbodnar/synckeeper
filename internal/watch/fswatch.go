package watch

// The file-watching backend, extracted behind an interface (W3): the fsnotify
// implementation lives here today; an FSEvents backend (cgo, macOS) slots in
// later without touching the sync loop. Correctness never depends on any event
// arriving (spec §8.1 — events are wake-ups only; the poll tick covers
// everything a backend misses, drops, or coalesces), so a backend is free to
// fail at creation (→ pure polling) or under fd pressure (→ the failure latch).

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"

	"github.com/macsimbodnar/synckeeper/internal/names"
)

// fsWatcher is a running file-watching backend. It wakes the sync loop on
// relevant local changes and holds whatever OS resources watching needs.
type fsWatcher interface {
	// refresh makes the watch set cover root and returns how many directories
	// could not be watched (fd pressure → the failure latch). A whole-tree
	// backend (FSEvents) watches root in one call and returns 0.
	refresh(root string) int
	// close releases every descriptor/stream the backend holds; its event
	// pump stops.
	close() error
	// needsRebuild reports whether the sync loop should periodically tear the
	// backend down and recreate it to bound a descriptor leak (W3.4). fsnotify's
	// kqueue leaks an fd when a watched file is deleted faster than its event is
	// processed, so it does; FSEvents holds no per-file descriptors and is left
	// running.
	needsRebuild() bool

	// name identifies the backend for reporting ("fsnotify", "fsevents"). It is
	// display-only: nothing in the loop branches on it (W15-U5).
	name() string
}

// newNotifyWatcher creates the raw fsnotify watcher — a test seam so R15 can
// inject the fd-pressure creation failure without exhausting real descriptors.
// Tests swap it only while no watcher goroutine is running.
var newNotifyWatcher = fsnotify.NewWatcher

// newBackend is the sole place a concrete backend is chosen (and a seam for
// tests). It creates the backend over root, performs the initial watch, and
// starts the event pump bound to ctx; `wake` fires on every relevant local
// change and `ignore` supplies the current ignore globs. Returns the count of
// directories that could not be watched (fd pressure → the latch).
var newBackend = newFSNotifyBackend

// fsnotifyBackend watches one directory at a time, refreshed by re-walking the
// tree. Its pump translates fsnotify events into wake-ups and registers
// newly-created directories immediately — files can land in them before the
// post-sync refresh.
type fsnotifyBackend struct {
	fw     *fsnotify.Watcher
	ignore func() []string
	wake   func()
}

func newFSNotifyBackend(ctx context.Context, root string, ignore func() []string, wake func()) (fsWatcher, int, error) {
	fw, err := newNotifyWatcher()
	if err != nil {
		return nil, 0, err
	}
	b := &fsnotifyBackend{fw: fw, ignore: ignore, wake: wake}
	failed := b.refresh(root)
	go b.pump(ctx)
	return b, failed, nil
}

func (b *fsnotifyBackend) close() error { return b.fw.Close() }

// needsRebuild is true: kqueue can leak a descriptor per watched file, so the
// loop periodically closes and recreates the watcher to release them.
func (b *fsnotifyBackend) needsRebuild() bool { return true }

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
			b.wake()
		case err, ok := <-b.fw.Errors:
			if !ok {
				return
			}
			slog.Warn("fsnotify error", "err", err)
		}
	}
}
