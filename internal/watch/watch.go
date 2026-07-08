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

// Run blocks until ctx is cancelled. Sync failures are logged and retried
// with backoff; the loop only exits on cancellation.
func (w *Watcher) Run(ctx context.Context) error {
	if w.Debounce <= 0 {
		w.Debounce = 500 * time.Millisecond
	}
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer fw.Close()
	w.syncWatches(fw)

	trigger := make(chan struct{}, 1)
	kick := func() {
		select {
		case trigger <- struct{}{}:
		default:
		}
	}
	debounce := time.AfterFunc(time.Hour, kick)
	debounce.Stop()

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

	ticker := time.NewTicker(w.Poll)
	defer ticker.Stop()
	backoff := w.Poll

	// Initial sync on startup: catches everything that happened while the
	// daemon was down, including local changes no event will ever fire for.
	kick()

	for {
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
			w.syncWatches(fw)
		}
	}
}

// syncWatches makes the fsnotify watch set match the current directory
// tree. fsnotify drops watches for deleted dirs on its own; stale Adds are
// harmless, so a simple re-walk suffices.
func (w *Watcher) syncWatches(fw *fsnotify.Watcher) {
	w.watchSubtree(fw, w.Eng.SyncDir)
}

func (w *Watcher) watchSubtree(fw *fsnotify.Watcher, root string) {
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
			slog.Debug("watch add failed", "path", p, "err", err)
		}
		return nil
	})
}
