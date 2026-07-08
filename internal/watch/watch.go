// Package watch runs the sync engine continuously: fsnotify events on the
// local tree and a remote polling timer both feed the same serialized sync
// loop — there is deliberately no second sync code path. Every cycle is a
// full engine.Sync (full local scan + changes.list poll), which at personal
// scale is cheap and makes dropped fsnotify events harmless: the next poll
// tick catches whatever was missed.
package watch

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/macsimbodnar/synckeeper/internal/engine"
	"github.com/macsimbodnar/synckeeper/internal/names"
)

// Watcher drives continuous sync for one engine.
type Watcher struct {
	Eng      *engine.Engine
	Poll     time.Duration // remote polling cadence
	Debounce time.Duration // quiet window after local events
}

// rebuildEvery: sync cycles between fsnotify watcher rebuilds. On kqueue a
// descriptor can leak when a watched file is deleted faster than its event
// is processed; closing the watcher releases every fd it holds, and the
// full-scan poll covers the swap window. ~500 cycles ≈ 6 h at the default
// 45 s poll, minutes under event storms — either way the leak stays bounded.
const rebuildEvery = 500

// Run blocks until ctx is cancelled. Sync failures are logged and retried
// with backoff; the loop only exits on cancellation.
func (w *Watcher) Run(ctx context.Context) error {
	if w.Debounce <= 0 {
		w.Debounce = 500 * time.Millisecond
	}
	fdLimitOnce.Do(raiseFDLimit)

	trigger := make(chan struct{}, 1)
	kick := func() {
		select {
		case trigger <- struct{}{}:
		default:
		}
	}
	debounce := time.AfterFunc(time.Hour, kick)
	debounce.Stop()

	fw, err := w.startNotifier(ctx, debounce)
	if err != nil {
		return err
	}
	defer func() { fw.Close() }()

	ticker := time.NewTicker(w.Poll)
	defer ticker.Stop()
	backoff := w.Poll

	// Initial sync on startup: catches everything that happened while the
	// daemon was down, including local changes no event will ever fire for.
	kick()

	for cycle := 1; ; cycle++ {
		select {
		case <-ctx.Done():
			return nil
		case <-trigger:
		case <-ticker.C:
		}

		res, err := w.Eng.Sync(ctx, engine.Options{})
		switch {
		case ctx.Err() != nil:
			return nil
		case err != nil:
			// Guards (mass delete, missing dir) land here too: keep the
			// daemon alive and loudly wait for the human.
			slog.Error("sync cycle failed; will retry", "err", err, "backoff", backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil
			}
			if backoff < 10*w.Poll {
				backoff *= 2
			}
		default:
			backoff = w.Poll
			if len(res.Plan) > 0 {
				slog.Info("sync cycle", "actions", len(res.Plan), "executed", res.Executed, "failed", res.Failed)
			} else {
				slog.Debug("sync cycle: no changes")
			}
			if cycle%rebuildEvery == 0 {
				fw.Close() // pump goroutine exits with the channel
				if fw, err = w.startNotifier(ctx, debounce); err != nil {
					return err
				}
				slog.Debug("rebuilt fsnotify watcher", "cycle", cycle)
			} else {
				w.syncWatches(fw)
			}
		}
	}
}

// startNotifier creates an fsnotify watcher over the whole tree and starts
// its event pump, which feeds the debounce timer until the watcher closes.
func (w *Watcher) startNotifier(ctx context.Context, debounce *time.Timer) (*fsnotify.Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w.syncWatches(fw)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-fw.Events:
				if !ok {
					return
				}
				if names.Ignored(filepath.Base(ev.Name), w.Eng.Cfg.Engine.Ignore) {
					continue
				}
				if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
					continue
				}
				// New directories must be watched immediately: files may land
				// in them before the post-sync watch refresh.
				if ev.Op&fsnotify.Create != 0 {
					if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
						w.watchSubtree(fw, ev.Name)
					}
				}
				debounce.Reset(w.Debounce)
			case err, ok := <-fw.Errors:
				if !ok {
					return
				}
				slog.Warn("fsnotify error", "err", err)
			}
		}
	}()
	return fw, nil
}

// fdLimitOnce: the rlimit is process-wide; one raise is enough.
var fdLimitOnce sync.Once

// syncWatches makes the fsnotify watch set match the current directory
// tree. fsnotify drops watches for deleted dirs on its own; stale Adds are
// harmless, so a simple re-walk suffices.
func (w *Watcher) syncWatches(fw *fsnotify.Watcher) {
	w.watchSubtree(fw, w.Eng.SyncDir)
}

func (w *Watcher) watchSubtree(fw *fsnotify.Watcher, root string) {
	failed := 0
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if names.Ignored(d.Name(), w.Eng.Cfg.Engine.Ignore) && p != root {
			return filepath.SkipDir
		}
		if err := fw.Add(p); err != nil {
			failed++
		}
		return nil
	})
	if failed > 0 {
		// Degraded but correct: the poll tick full-scans everything the
		// missing watches would have announced.
		slog.Warn("some directories could not be watched; relying on polling for them",
			"failed", failed, "poll_interval", w.Poll)
	}
}
