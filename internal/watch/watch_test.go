package watch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	t.Cleanup(func() { cancel(); <-done })
	return cancel
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
