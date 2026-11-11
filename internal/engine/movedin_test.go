package engine

// S018 end to end: a folder moved into the Drive tree whose content walk
// stops part-way — by error or by process death — downloads on a later
// cycle, and a folder moved out and back plans no local delete.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/remotedelta"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// listFaultAt fails List: the at-th call when at > 0, else the first call
// naming failID. When crash is set it first takes the machine DB's crash
// image — the committed state a process killed at that instant leaves.
type listFaultAt struct {
	driveclient.Client
	at, calls int
	failID    string
	failed    bool
	crash     func()
}

func (l *listFaultAt) List(ctx context.Context, parentID string) ([]driveclient.File, error) {
	l.calls++
	if (l.at > 0 && l.calls == l.at) || (l.failID != "" && l.failID == parentID && !l.failed) {
		l.failed = true
		if l.crash != nil {
			l.crash()
		}
		return nil, errors.New("dial tcp: connection reset by peer")
	}
	return l.Client.List(ctx, parentID)
}

// dbPath is where newMachine put the machine's state DB.
func (m *machine) dbPath() string { return filepath.Join(filepath.Dir(m.dir), "state.db") }

// takeCrashImage copies the committed state of m's DB to dst.
func takeCrashImage(t *testing.T, m *machine, dst string) {
	t.Helper()
	h, err := sql.Open("sqlite", m.dbPath())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if _, err := h.Exec(`vacuum into ?`, dst); err != nil {
		t.Fatal(err)
	}
}

// resumeFromImage makes m continue from a crash image, as a restarted
// process would.
func resumeFromImage(t *testing.T, m *machine, image string) {
	t.Helper()
	db, err := statedb.Open(image)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	m.db = db
	m.eng.DB = db
}

func countDeletes(plan []reconcile.Action) int {
	n := 0
	for _, a := range plan {
		if a.Type == reconcile.TrashRemote || a.Type == reconcile.QuarantineLocal || a.Type == reconcile.Forget {
			n++
		}
	}
	return n
}

// The reviewer's shape through the engine: the cycle whose walk of the
// moved-in folder fails reports the error; the next cycle downloads the
// contents. Before the fix they never downloaded.
func TestS018MovedInFolderDownloadsAfterFailedWalk(t *testing.T) {
	for _, crash := range []bool{false, true} {
		t.Run(fmt.Sprintf("crash=%v", crash), func(t *testing.T) {
			ctx := context.Background()
			fake, root := newWorld(t)
			box, err := fake.Mkdir(ctx, driveclient.FakeRootID, "box")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fake.Upload(ctx, box.ID, "inner.txt", strings.NewReader("payload"), 7); err != nil {
				t.Fatal(err)
			}
			sub, err := fake.Mkdir(ctx, box.ID, "sub")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fake.Upload(ctx, sub.ID, "deep.txt", strings.NewReader("deep"), 4); err != nil {
				t.Fatal(err)
			}
			a := newMachine(t, "a", fake, root)
			a.sync(t)

			if _, err := fake.Move(ctx, box.ID, root, "box"); err != nil {
				t.Fatal(err)
			}
			k := &listFaultAt{Client: fake, failID: box.ID}
			image := filepath.Join(t.TempDir(), "crash.db")
			if crash {
				k.crash = func() { takeCrashImage(t, a, image) }
			}
			a.eng.Client = k
			if _, err := a.eng.Sync(ctx, Options{}); err == nil {
				t.Fatal("the cycle whose walk failed should have reported it")
			}
			a.eng.Client = fake
			if crash {
				resumeFromImage(t, a, image)
			}

			a.sync(t)
			if got := a.read(t, "box/inner.txt"); got != "payload" {
				t.Errorf("box/inner.txt = %q, want the Drive content", got)
			}
			if got := a.read(t, "box/sub/deep.txt"); got != "deep" {
				t.Errorf("box/sub/deep.txt = %q, want the Drive content", got)
			}
		})
	}
}

// The deletion route the S009 reviewer guessed (S018 excludes): a synced
// folder is moved out of the tree; that cycle prunes its rows from the mirror
// but its local deletes never run — held by the mass-delete guard, or the
// process dies after the refresh. The folder is moved back and the re-walk
// stops at any List, by error or crash. No later cycle may plan a delete
// while Drive still has the files.
func TestS018MovedOutAndBackInterruptedDeletesNothing(t *testing.T) {
	ctx := context.Background()
	type world struct {
		fake   *driveclient.Fake
		root   string
		a      *machine
		before map[string]string
	}
	setup := func(t *testing.T, route string) *world {
		t.Helper()
		fake, root := newWorld(t)
		a := newMachine(t, "a", fake, root)
		a.write(t, "top.txt", "top")
		for i := 0; i < 10; i++ {
			a.write(t, fmt.Sprintf("box/f%02d.txt", i), fmt.Sprintf("file %d", i))
		}
		a.write(t, "box/sub/s.txt", "sub")
		a.write(t, "box/sub/deeper/d.txt", "deeper")
		a.sync(t)
		if res := a.sync(t); len(res.Plan) != 0 {
			t.Fatalf("setup: second cycle planned %d actions, want idle", len(res.Plan))
		}
		before := a.listTree(t)

		var boxID string
		children, err := fake.List(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range children {
			if c.Name == "box" {
				boxID = c.ID
			}
		}
		if _, err := fake.Move(ctx, boxID, driveclient.FakeRootID, "box"); err != nil {
			t.Fatal(err)
		}
		switch route {
		case "guard":
			// No bin: twelve local deletes are held for confirmation.
			a.bin.Unavailable = true
			if _, err := a.eng.Sync(ctx, Options{}); err == nil {
				t.Fatal("setup: the move-out cycle's deletes were not held by the guard")
			}
			a.bin.Unavailable = false
		case "crash":
			// The committed state of a cycle killed right after its refresh.
			if err := remotedelta.Refresh(ctx, fake, a.db, root); err != nil {
				t.Fatal(err)
			}
		}
		if got := len(a.listTree(t)); got != len(before) {
			t.Fatalf("setup: %d of %d local files left after the move-out cycle", got, len(before))
		}
		if _, err := fake.Move(ctx, boxID, root, "box"); err != nil {
			t.Fatal(err)
		}
		return &world{fake: fake, root: root, a: a, before: before}
	}

	for _, route := range []string{"guard", "crash"} {
		clean := setup(t, route)
		counter := &listFaultAt{Client: clean.fake}
		clean.a.eng.Client = counter
		res := clean.a.sync(t)
		if n := countDeletes(res.Plan); n != 0 {
			t.Fatalf("%s: a clean move-back cycle planned %d deletes", route, n)
		}
		if counter.calls < 3 {
			t.Fatalf("%s: a clean move-back cycle made %d List calls; box has 3 folders", route, counter.calls)
		}

		for at := 1; at <= counter.calls; at++ {
			for _, crash := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/list%d/crash=%v", route, at, crash), func(t *testing.T) {
					w := setup(t, route)
					k := &listFaultAt{Client: w.fake, at: at}
					image := filepath.Join(t.TempDir(), "crash.db")
					if crash {
						k.crash = func() { takeCrashImage(t, w.a, image) }
					}
					w.a.eng.Client = k
					res, err := w.a.eng.Sync(ctx, Options{})
					if err == nil {
						t.Fatal("the cycle whose walk stopped should have reported it")
					}
					if res != nil && countDeletes(res.Plan) != 0 {
						t.Fatalf("the interrupted cycle planned %d deletes", countDeletes(res.Plan))
					}
					w.a.eng.Client = w.fake
					if crash {
						resumeFromImage(t, w.a, image)
					}

					for i := 0; i < 2; i++ {
						res := w.a.sync(t)
						if n := countDeletes(res.Plan); n != 0 {
							t.Fatalf("cycle %d after the interrupted walk planned %d deletes: %v", i+1, n, res.Plan)
						}
					}
					after := w.a.listTree(t)
					if len(after) != len(w.before) {
						t.Fatalf("%d of %d local files left", len(after), len(w.before))
					}
					for p, content := range w.before {
						if after[p] != content {
							t.Errorf("%s = %q, want %q", p, after[p], content)
						}
					}
				})
			}
		}
	}
}
