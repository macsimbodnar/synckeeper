package watch

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// rootRecordingBackend remembers every root the loop hands a backend.
type rootRecordingBackend struct {
	mu    *sync.Mutex
	roots *[]string
}

func (r *rootRecordingBackend) refresh(root string) int {
	r.mu.Lock()
	*r.roots = append(*r.roots, root)
	r.mu.Unlock()
	return 0
}
func (r *rootRecordingBackend) close() error       { return nil }
func (r *rootRecordingBackend) needsRebuild() bool { return false }
func (r *rootRecordingBackend) name() string       { return "fake" }

// S007: with a symlinked sync_dir the watch backends must be given the
// directory the link names. The fsnotify backend walks its root to add one
// watch per directory, and a walk does not descend a symlinked root — handed
// the link, it would watch nothing below it and the daemon would fall back to
// the poll tick without saying so.
func TestWatchBackendsGetTheResolvedSyncDir(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	a.write(t, "anchor.txt", "keep the dir non-empty")
	link := filepath.Join(t.TempDir(), "Synckeeper")
	if err := os.Symlink(a.dir, link); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	a.eng.SyncDir = link

	var mu sync.Mutex
	var roots []string
	newBackend = func(_ context.Context, root string, _ func() []string, _ backendHooks) (fsWatcher, int, error) {
		mu.Lock()
		roots = append(roots, root)
		mu.Unlock()
		return &rootRecordingBackend{mu: &mu, roots: &roots}, 0, nil
	}
	t.Cleanup(func() { newBackend = newFSNotifyBackend })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	w := &Watcher{Eng: a.eng, Poll: 15 * time.Millisecond, Debounce: 5 * time.Millisecond}
	go func() {
		defer close(done)
		if err := w.Run(ctx); err != nil {
			t.Errorf("Run: %v", err)
		}
	}()
	t.Cleanup(func() { cancel(); <-done })

	// One creation plus at least one post-cycle refresh.
	waitFor(t, "backend creation and a refresh", 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(roots) >= 2
	})
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	for _, r := range roots {
		if r != want {
			t.Fatalf("a backend was handed %q, want the resolved sync dir %q (all roots: %v)", r, want, roots)
		}
	}
}
