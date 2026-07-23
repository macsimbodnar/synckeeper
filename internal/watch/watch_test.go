package watch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/macsimbodnar/synckeeper/internal/config"
	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/engine"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

type machine struct {
	name string
	dir  string
	db   *statedb.DB
	eng  *engine.Engine
}

func newWorld(t *testing.T) (*driveclient.Fake, string) {
	t.Helper()
	fake := driveclient.NewFake()
	folder, err := fake.Mkdir(context.Background(), driveclient.FakeRootID, "Synckeeper")
	if err != nil {
		t.Fatal(err)
	}
	return fake, folder.ID
}

func newMachine(t *testing.T, name string, fake *driveclient.Fake, rootID string) *machine {
	t.Helper()
	base := t.TempDir()
	dir := filepath.Join(base, "sync")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := statedb.Open(filepath.Join(base, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	token, err := fake.StartPageToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMeta(statedb.MetaPageToken, token)
	db.SetMeta(statedb.MetaRootFolderID, rootID) // mirrors init; doctor reads it
	cfg := config.Default()
	cfg.Engine.MachineName = name
	return &machine{
		name: name,
		dir:  dir,
		db:   db,
		eng: &engine.Engine{
			DB: db, Client: fake, Cfg: cfg, SyncDir: dir,
			QuarantineDir: filepath.Join(base, "quarantine"), RootID: rootID,
		},
	}
}

func (m *machine) write(t *testing.T, rel, content string) {
	t.Helper()
	p := filepath.Join(m.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// startWatcher runs a daemon loop for m and returns a stop func that cancels
// it AND waits for Run to fully exit. The wait is load-bearing: callers (the
// soak's settle phase) drive engine.Sync directly right after stopping, and
// the engine requires cycles to be serialized — an in-flight daemon cycle
// racing a direct Sync can double-plan an upload and mint a duplicate-name
// pair on Drive (W3 adversarial check, 2026-07-23).
func startWatcher(t *testing.T, m *machine, poll time.Duration) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	w := &Watcher{Eng: m.eng, Poll: poll, Debounce: 50 * time.Millisecond}
	go func() {
		defer close(done)
		if err := w.Run(ctx); err != nil {
			t.Errorf("[%s] watcher: %v", m.name, err)
		}
	}()
	stop := func() { cancel(); <-done }
	t.Cleanup(stop) // idempotent: cancel is, and done stays closed
	return stop
}

func waitFor(t *testing.T, what string, deadline time.Duration, cond func() bool) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The stop func startWatcher returns must not return until Run has fully
// exited: the soak's settle phase calls engine.Sync directly the moment the
// watchers are stopped, and a still-in-flight daemon cycle would run
// concurrently with those syncs on the same engine — which the engine forbids
// (cycles are serialized; two concurrent cycles can double-plan an upload and
// mint a duplicate-name pair on Drive). rec.stop() runs before Run returns, so
// "stopped is already recorded when stop() returns" is the observable proof.
func TestStopWaitsForDaemonExit(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	stop := startWatcher(t, a, 25*time.Millisecond)

	// Put a cycle in flight: a burst of files, then a beat for the debounce
	// to fire and the sync to start.
	for i := 0; i < 50; i++ {
		a.write(t, fmt.Sprintf("burst/f%02d.txt", i), "payload")
	}
	time.Sleep(60 * time.Millisecond)
	stop()

	ds, err := a.db.GetDaemonStatus()
	if err != nil {
		t.Fatal(err)
	}
	if ds.Running || ds.Mode != ModeStopped {
		t.Fatalf("stop() returned while the daemon reports running=%v mode=%q — settle-phase syncs would race the in-flight cycle", ds.Running, ds.Mode)
	}
}

// A local write is picked up by fsnotify well before the poll tick.
func TestWatcherReactsToLocalEvents(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	startWatcher(t, a, time.Hour) // poll effectively off: only fsnotify can trigger

	// Let the initial watches settle, then drop a file in a NEW subdir —
	// this also proves new dirs get watched on the fly.
	time.Sleep(100 * time.Millisecond)
	a.write(t, "newdir/hello.txt", "via fsnotify")

	waitFor(t, "upload via fsnotify", 5*time.Second, func() bool {
		children, err := fake.List(context.Background(), root)
		if err != nil {
			return false
		}
		for _, c := range children {
			if c.Name == "newdir" && c.IsDir() {
				sub, _ := fake.List(context.Background(), c.ID)
				for _, s := range sub {
					if s.Name == "hello.txt" {
						return true
					}
				}
			}
		}
		return false
	})
}

// Changes made while the watcher was NOT running — i.e. changes no fsnotify
// event will ever announce — are picked up by the full scan each cycle
// performs. This is the dropped-event recovery guarantee.
func TestWatcherCatchesUnannouncedLocalChanges(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)

	// Written before the watcher exists: no event will fire for this.
	a.write(t, "silent/unannounced.txt", "no event ever fired")
	startWatcher(t, a, time.Hour)

	waitFor(t, "upload via full scan", 5*time.Second, func() bool {
		children, err := fake.List(context.Background(), root)
		if err != nil {
			return false
		}
		for _, c := range children {
			if c.Name == "silent" && c.IsDir() {
				sub, _ := fake.List(context.Background(), c.ID)
				return len(sub) == 1 && sub[0].Name == "unannounced.txt"
			}
		}
		return false
	})
}

// A remote change arrives via the polling loop without any local event.
func TestWatcherPollsRemote(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	startWatcher(t, a, 100*time.Millisecond)

	if _, err := fake.Upload(context.Background(), root, "polled.txt", stringsReader("remote content"), 14); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "download via polling", 5*time.Second, func() bool {
		raw, err := os.ReadFile(filepath.Join(a.dir, "polled.txt"))
		return err == nil && string(raw) == "remote content"
	})
}

// R15 (A6, spec §8.1/§10): a watcher rebuild failure — fsnotify.NewWatcher
// failing under fd pressure — must degrade the daemon to polling-only and
// keep syncing, never exit Run (was: `return err` killed the daemon; the
// pollingOnly branch already degraded on the identical error, which was the
// tell). The rebuild cadence then restores watching once creation succeeds.
func TestR15WatcherRebuildFailureDegradesToPolling(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)

	// Call 1 (startup) succeeds; call 2 (the rebuild) fails like fd
	// exhaustion; call 3+ (the periodic retry) succeeds again.
	var calls atomic.Int32
	newNotifyWatcher = func() (*fsnotify.Watcher, error) {
		if calls.Add(1) == 2 {
			return nil, errors.New("injected: too many open files")
		}
		return fsnotify.NewWatcher()
	}
	t.Cleanup(func() { newNotifyWatcher = fsnotify.NewWatcher })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	w := &Watcher{Eng: a.eng, Poll: 50 * time.Millisecond,
		Debounce: 10 * time.Millisecond, rebuildCadence: 3}
	go func() {
		defer close(done)
		if err := w.Run(ctx); err != nil {
			t.Errorf("Run exited: %v (a rebuild failure must degrade, not kill)", err)
		}
	}()
	t.Cleanup(func() { cancel(); <-done })

	// The poll ticker drives cycles to the rebuild at cycle 3; the injected
	// failure must surface as polling-only, not an exit.
	waitFor(t, "degraded to polling-only", 5*time.Second, func() bool {
		ds, err := a.db.GetDaemonStatus()
		return err == nil && ds.Mode == ModePollingOnly
	})

	// Still syncing while degraded: polling covers the tree.
	a.write(t, "while_degraded.txt", "polling covers me")
	waitFor(t, "sync while polling-only", 5*time.Second, func() bool {
		return remoteHasName(t, fake, root, "while_degraded.txt")
	})

	// The rebuild cadence retries watcher creation and watching comes back.
	waitFor(t, "watching restored", 5*time.Second, func() bool {
		ds, err := a.db.GetDaemonStatus()
		return err == nil && ds.Mode == ModeWatching
	})
}

// R15, the same shape at the startup site: a daemon that cannot create a
// watcher at launch starts polling-only (spec §10's universal fallback)
// instead of refusing to run, and syncs via the poll.
func TestR15StartupWatcherFailureStartsPollingOnly(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)

	newNotifyWatcher = func() (*fsnotify.Watcher, error) {
		return nil, errors.New("injected: too many open files")
	}
	t.Cleanup(func() { newNotifyWatcher = fsnotify.NewWatcher })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	w := &Watcher{Eng: a.eng, Poll: 50 * time.Millisecond, Debounce: 10 * time.Millisecond}
	go func() {
		defer close(done)
		if err := w.Run(ctx); err != nil {
			t.Errorf("Run exited: %v (startup watcher failure must degrade, not exit)", err)
		}
	}()
	t.Cleanup(func() { cancel(); <-done })

	waitFor(t, "polling-only from launch", 5*time.Second, func() bool {
		ds, err := a.db.GetDaemonStatus()
		return err == nil && ds.Mode == ModePollingOnly
	})
	a.write(t, "no_watcher.txt", "poll finds me")
	waitFor(t, "sync without a watcher", 5*time.Second, func() bool {
		return remoteHasName(t, fake, root, "no_watcher.txt")
	})
}
