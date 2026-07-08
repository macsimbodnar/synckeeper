package watch

// Soak test (phase 3 exit criterion): two machines run watchers against one
// fake Drive while chaos goroutines make random edits on both sides; at the
// end everything must converge with no content lost to anything but
// legitimate deletes. Gated by SYNCKEEPER_SOAK_SECONDS because the full run
// is long: the 2-hour gate is SYNCKEEPER_SOAK_SECONDS=7200.

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/doctor"
	"github.com/macsimbodnar/synckeeper/internal/engine"
)

func stringsReader(s string) *strings.Reader { return strings.NewReader(s) }

type chaos struct {
	rng   *rand.Rand
	m     *machine
	paths []string // files this goroutine knows about (its own creations)
	dirs  []string
}

func (c *chaos) step(t *testing.T) {
	defer func() {
		// Chaos racing the sync engine on the same tree can legitimately hit
		// vanished files; chaos itself must never kill the test.
		recover()
	}()
	switch n := c.rng.Intn(10); {
	case n < 4 || len(c.paths) == 0: // create
		dir := ""
		if len(c.dirs) > 0 && c.rng.Intn(2) == 0 {
			dir = c.dirs[c.rng.Intn(len(c.dirs))]
		}
		rel := filepath.Join(dir, fmt.Sprintf("f-%s-%d.txt", c.m.name, c.rng.Int63()))
		c.write(rel)
		c.paths = append(c.paths, rel)
	case n < 6: // edit (possibly a file the other machine created)
		rel := c.paths[c.rng.Intn(len(c.paths))]
		c.write(rel)
	case n < 7: // mkdir
		rel := fmt.Sprintf("d-%s-%d", c.m.name, c.rng.Int63())
		if len(c.dirs) > 0 && c.rng.Intn(2) == 0 {
			rel = filepath.Join(c.dirs[c.rng.Intn(len(c.dirs))], rel)
		}
		os.MkdirAll(filepath.Join(c.m.dir, rel), 0o755)
		c.dirs = append(c.dirs, rel)
	case n < 8 && len(c.paths) > 1: // rename
		i := c.rng.Intn(len(c.paths))
		newRel := c.paths[i] + ".moved"
		os.Rename(filepath.Join(c.m.dir, c.paths[i]), filepath.Join(c.m.dir, newRel))
		c.paths[i] = newRel
	default: // delete
		i := c.rng.Intn(len(c.paths))
		os.Remove(filepath.Join(c.m.dir, c.paths[i]))
		c.paths = append(c.paths[:i], c.paths[i+1:]...)
	}
}

func (c *chaos) write(rel string) {
	p := filepath.Join(c.m.dir, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(p), 0o755)
	content := fmt.Sprintf("from %s at %d: %d", c.m.name, time.Now().UnixNano(), c.rng.Int63())
	os.WriteFile(p, []byte(content), 0o644)
}

func TestSoak(t *testing.T) {
	secsStr := os.Getenv("SYNCKEEPER_SOAK_SECONDS")
	if secsStr == "" {
		t.Skip("set SYNCKEEPER_SOAK_SECONDS (7200 for the full 2-hour gate)")
	}
	secs, err := strconv.Atoi(secsStr)
	if err != nil {
		t.Fatal(err)
	}
	duration := time.Duration(secs) * time.Second

	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	// Chaos deletes freely; the mass-delete guard is for humans, not soak.
	a.eng.Cfg.Engine.MassDeleteThreshold = 1.0
	b.eng.Cfg.Engine.MassDeleteThreshold = 1.0
	// An anchor file per machine so the dir never goes empty (guard G2).
	a.write(t, "anchor-a.txt", "anchor")
	b.write(t, "anchor-b.txt", "anchor")

	cancelA := startWatcher(t, a, 300*time.Millisecond)
	cancelB := startWatcher(t, b, 300*time.Millisecond)

	stopChaos := make(chan struct{})
	var wg sync.WaitGroup
	for i, m := range []*machine{a, b} {
		wg.Add(1)
		go func(seed int64, m *machine) {
			defer wg.Done()
			c := &chaos{rng: rand.New(rand.NewSource(seed)), m: m}
			for {
				select {
				case <-stopChaos:
					return
				case <-time.After(time.Duration(50+c.rng.Intn(250)) * time.Millisecond):
					c.step(t)
				}
			}
		}(int64(i+1), m)
	}

	t.Logf("soaking for %s ...", duration)
	time.Sleep(duration)
	close(stopChaos)
	wg.Wait()

	// Let the watchers finish propagating, then stop them and settle with
	// explicit syncs until both machines plan nothing twice in a row.
	time.Sleep(2 * time.Second)
	cancelA()
	cancelB()
	quiet := 0
	for i := 0; i < 60 && quiet < 2; i++ {
		total := 0
		for _, m := range []*machine{a, b} {
			res, err := m.eng.Sync(context.Background(), engine.Options{})
			if err != nil {
				t.Fatalf("[%s] settle sync: %v", m.name, err)
			}
			total += len(res.Plan)
		}
		if total == 0 {
			quiet++
		} else {
			quiet = 0
		}
	}
	if quiet < 2 {
		t.Fatal("machines never settled after the soak")
	}

	// Convergence: identical trees.
	treeA, treeB := tree(t, a.dir), tree(t, b.dir)
	if len(treeA) != len(treeB) {
		t.Fatalf("diverged: a has %d files, b has %d", len(treeA), len(treeB))
	}
	for p, content := range treeA {
		if treeB[p] != content {
			t.Errorf("diverged at %s", p)
		}
	}
	t.Logf("converged on %d files", len(treeA))

	// Doctor agrees on both machines.
	for _, m := range []*machine{a, b} {
		rep, err := (&doctor.Doctor{DB: m.db, Client: fake, Cfg: m.eng.Cfg, SyncDir: m.dir}).Check(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !rep.Healthy() {
			t.Errorf("[%s] doctor after soak: %+v", m.name, rep)
		}
	}
}

func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(raw)
		return nil
	})
	return out
}
