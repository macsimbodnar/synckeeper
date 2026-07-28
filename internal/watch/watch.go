// Package watch runs the sync engine continuously: local file-watch events
// (from a pluggable fsWatcher backend — fsnotify today, FSEvents later) and a
// remote polling timer both feed the same serialized sync loop — there is
// deliberately no second sync code path. Every cycle is a full engine.Sync
// (full local scan + changes.list poll), which at personal scale is cheap and
// makes dropped events harmless: the next poll tick catches whatever was
// missed.
package watch

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/control"
	"github.com/macsimbodnar/synckeeper/internal/engine"
	"github.com/macsimbodnar/synckeeper/internal/trash"
)

// Watcher drives continuous sync for one engine.
type Watcher struct {
	Eng      *engine.Engine
	Poll     time.Duration // remote polling cadence
	Debounce time.Duration // quiet window after local events

	// ControlSocket, if set, is the path to the Unix-domain socket the
	// daemon serves control commands on; ConfigDir is where `reload` re-reads
	// config.toml. Both empty in tests that don't exercise control.
	ControlSocket string
	ConfigDir     string

	// rebuildCadence overrides rebuildEvery when nonzero, so tests can
	// reach the rebuild path without driving 500 cycles.
	rebuildCadence int

	// ignore is the published ignore-glob snapshot (spec §8.3, R14). The
	// sync loop owns the config, but the watcher backend's event pump reads
	// the globs on every event from its own goroutine — so a `reload` must
	// publish a new snapshot here, never write the slice in place.
	ignore atomic.Pointer[[]string]
}

// publishIgnore makes globs the snapshot every off-loop reader sees.
func (w *Watcher) publishIgnore(globs []string) {
	w.ignore.Store(&globs)
}

// ignoreGlobs is safe from any goroutine. Before Run publishes (direct
// applyReload in tests) it falls back to the loop-owned config, which is
// single-goroutine in that situation.
func (w *Watcher) ignoreGlobs() []string {
	if p := w.ignore.Load(); p != nil {
		return *p
	}
	return w.Eng.Cfg.Engine.Ignore
}

// rebuildEvery: sync cycles between watcher rebuilds, for backends that report
// needsRebuild (fsnotify). On kqueue a descriptor can leak when a watched file
// is deleted faster than its event is processed; closing the watcher releases
// every fd it holds, and the full-scan poll covers the swap window. ~500 cycles
// ≈ 6 h at the default 45 s poll, minutes under event storms — either way the
// leak stays bounded. A backend that holds no per-file descriptors (FSEvents)
// reports needsRebuild=false and is left running instead (W3.4).
const rebuildEvery = 500

// Run blocks until ctx is cancelled. Sync failures are logged and retried
// with backoff; the loop only exits on cancellation.
func (w *Watcher) Run(ctx context.Context) error {
	if w.Debounce <= 0 {
		w.Debounce = 500 * time.Millisecond
	}
	w.publishIgnore(w.Eng.Cfg.Engine.Ignore)
	fdLimitOnce.Do(raiseFDLimit)

	rec := newRecorder(w.Eng.DB)
	defer rec.stop()
	go rec.heartbeat(ctx)

	trigger := make(chan struct{}, 1)
	kick := func() {
		select {
		case trigger <- struct{}{}:
		default:
		}
	}
	debounce := time.AfterFunc(time.Hour, kick)
	debounce.Stop()

	// live is read-only reporting for the dashboard (`stat`, W15-U5). Nothing in
	// the sync path reads it.
	live := newLiveState(w.Poll, w.Debounce)

	// Control socket: sync-now and reload hand work to this loop via channels
	// so the loop stays the sole owner of the engine, ticker, and config.
	syncNow := make(chan engine.Options, 1)
	reloadCh := make(chan chan reloadResult)
	if w.ControlSocket != "" {
		if ln, err := listenControl(w.ControlSocket); err != nil {
			// A missing control socket is a degraded convenience, not a
			// reason to stop syncing; the daemon runs on without it.
			slog.Warn("control socket unavailable; sync/pause/resume/reload won't reach this daemon", "err", err)
		} else {
			defer func() { ln.Close(); os.Remove(w.ControlSocket) }()
			go control.Serve(ctx, ln, w.controlHandler(syncNow, reloadCh, rec, live))
			slog.Debug("control socket listening", "path", w.ControlSocket)
		}
	}

	// Where deletions arriving from Drive will land, said once at startup
	// (W14-M2). With no bin they go to the private quarantine instead, which
	// is also the only remaining case where a mass deletion is held for
	// confirmation — a standing condition, so it is stated up front rather
	// than discovered from a blocked cycle.
	if !trash.Available() {
		slog.Warn("no system bin on this machine; deletions arriving from Drive will be rescued to the quarantine, and a mass deletion will wait for --confirm-deletes",
			"reason", trash.Describe())
	} else {
		slog.Info("deletions arriving from Drive go to the system bin", "destination", trash.Describe())
	}

	var latch failureLatch
	pollingOnly := false
	fw, failed, err := w.startNotifier(ctx, debounce, live)
	if err != nil {
		// Spec §10: watching is best-effort and the daemon falls back to
		// pure polling — including at launch (fd pressure, or a platform
		// with no working backend). The rebuild cadence retries creation.
		slog.Warn("file watching unavailable; relying on polling and retrying periodically",
			"err", err, "poll_interval", w.Poll)
		pollingOnly = true
	}
	defer func() {
		if fw != nil {
			fw.close()
		}
	}()
	if !pollingOnly {
		pollingOnly = w.latchIfNeeded(&latch, failed, &fw)
	}

	cadence := rebuildEvery
	if w.rebuildCadence > 0 {
		cadence = w.rebuildCadence
	}
	ticker := time.NewTicker(w.Poll)
	defer ticker.Stop()
	backoff := w.Poll

	// Initial sync on startup: catches everything that happened while the
	// daemon was down, including local changes no event will ever fire for.
	kick()

	for cycle := 1; ; cycle++ {
		var opts engine.Options
		forced := false
		select {
		case <-ctx.Done():
			return nil
		case <-trigger:
		case now := <-ticker.C:
			live.noteTick(now)
		case opts = <-syncNow: // control `sync`: run now, honor its options
			forced = true
		case respCh := <-reloadCh: // control `reload`: hot-swap config, no cycle
			respCh <- w.applyReload(ticker, live)
			continue
		}
		// While paused, automatic triggers are skipped; only an explicit
		// control `sync` (forced) still runs.
		if rec.isPaused() && !forced {
			continue
		}

		// The daemon never self-confirms a mass delete and never blocks the
		// whole cycle on it: it syncs everything else and surfaces the block
		// in status (spec §8.1).
		opts.DeferMassDelete = true
		start := time.Now()
		res, err := w.Eng.Sync(ctx, opts)
		dur := time.Since(start)
		switch {
		case ctx.Err() != nil:
			return nil
		case err != nil:
			// Hard errors (missing/unreadable sync dir, remote refresh failure,
			// ...): keep the daemon alive, back off, and wait loudly. A
			// mass-delete guard no longer lands here — the daemon defers it and
			// syncs everything else (recorded via res.GuardBlocked below).
			rec.cycleDone(res, dur, err, ModeBackoff, time.Now().Add(backoff), false, "")
			rec.recordError(err)
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
			switch {
			case pollingOnly:
				if cycle%cadence != 0 {
					break // stay on polling; retry only at rebuild cadence
				}
				nfw, failed, err := w.startNotifier(ctx, debounce, live)
				if err != nil || failed > 0 {
					if nfw != nil {
						nfw.close()
					}
					break
				}
				fw, pollingOnly, latch = nfw, false, failureLatch{}
				noteBackend(live, fw, pollingOnly)
				slog.Info("file watching restored; back to event-driven sync")
			case cycle%cadence == 0 && fw.needsRebuild():
				fw.close() // pump goroutine exits with the channel
				if fw, failed, err = w.startNotifier(ctx, debounce, live); err != nil {
					// Same degradation as the pollingOnly branch above on
					// the identical error (spec §8.1 "without exiting",
					// §10's polling fallback): the watcher is a wake-up
					// optimization, never a dependency, and the rebuild
					// cadence retries creation.
					fw, pollingOnly, latch = nil, true, failureLatch{}
					noteBackend(live, fw, pollingOnly)
					slog.Warn("watcher rebuild failed; relying on polling and retrying periodically",
						"err", err, "poll_interval", w.Poll)
					break
				}
				slog.Debug("rebuilt fsnotify watcher", "cycle", cycle)
				pollingOnly = w.latchIfNeeded(&latch, failed, &fw)
				noteBackend(live, fw, pollingOnly)
			default:
				pollingOnly = w.latchIfNeeded(&latch, fw.refresh(w.Eng.SyncDir), &fw)
				noteBackend(live, fw, pollingOnly)
			}
			mode := ModeWatching
			if pollingOnly {
				mode = ModePollingOnly
			}
			if rec.isPaused() {
				mode = ModePaused
			}
			if res.GuardBlocked {
				slog.Warn("mass-delete guard blocked deletions; synced everything else and waiting for --confirm-deletes",
					"reason", res.GuardReason)
			}
			rec.cycleDone(res, dur, nil, mode, time.Now().Add(w.Poll), res.GuardBlocked, res.GuardReason)
			rec.recordActivity(res)
		}
	}
}

// latchIfNeeded records a watch-refresh outcome; on repeated failures it
// shuts the watcher down (releasing every descriptor it holds) and switches
// to pure polling. The rebuild cadence periodically retries file watching.
func (w *Watcher) latchIfNeeded(latch *failureLatch, failed int, fw *fsWatcher) bool {
	if failed > 0 && latch.consecutive == 0 {
		slog.Warn("some directories could not be watched; polling covers them",
			"failed", failed, "poll_interval", w.Poll)
	}
	if !latch.note(failed) {
		return false
	}
	(*fw).close()
	*fw = nil
	slog.Warn("file watching disabled after repeated failures (out of file descriptors?); "+
		"relying on polling and retrying periodically", "poll_interval", w.Poll)
	return true
}

// startNotifier creates the file-watching backend over the whole tree and
// starts its event pump, which kicks the debounce timer until the backend
// closes. It also returns how many directories could not be watched. The
// backend is chosen behind the fsWatcher interface (W3), so the loop below is
// unchanged when an FSEvents backend replaces fsnotify.
func (w *Watcher) startNotifier(ctx context.Context, debounce *time.Timer, live *liveState) (fsWatcher, int, error) {
	wake := func() {
		if live != nil {
			live.noteWake(time.Now())
		}
		debounce.Reset(w.Debounce)
	}
	return newBackend(ctx, w.Eng.SyncDir, w.ignoreGlobs, wake)
}

// fdLimitOnce: the rlimit is process-wide; one raise is enough.
var fdLimitOnce sync.Once

// watchFailureLatch: consecutive cycles with watch-registration failures
// after which fsnotify is shut down in favor of pure polling. Failures here
// mean fd pressure (the tree needs more descriptors than the process has);
// keeping the watcher would starve the sync itself, and retrying every
// cycle just spams the log. Polling alone is slower but fully correct.
const watchFailureLatch = 3

// failureLatch counts consecutive failing watch refreshes.
type failureLatch struct{ consecutive int }

// note records one refresh outcome and reports whether to latch.
func (l *failureLatch) note(failed int) bool {
	if failed == 0 {
		l.consecutive = 0
		return false
	}
	l.consecutive++
	return l.consecutive >= watchFailureLatch
}

// noteBackend keeps the reported watch mode in step with the live one. Called
// wherever the loop creates, rebuilds, or gives up on a backend, so `stat`
// cannot claim "watching" while the loop is polling (the honesty bug W3-adv-3
// fixed for `status`, kept from returning here).
func noteBackend(live *liveState, fw fsWatcher, pollingOnly bool) {
	if live == nil {
		return
	}
	if fw == nil || pollingOnly {
		live.setBackend("polling", true)
		return
	}
	live.setBackend(fw.name(), false)
}
