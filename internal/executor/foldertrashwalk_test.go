package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// S021 round 2: the folder-trash gate's walk. The folder being trashed is
// listed last, after every subfolder listing has returned, so the shallowest
// level — where a user most likely adds a file — has the freshest listing;
// subfolder listings run concurrently, still one List per folder.

// walkClient records the order of List and Trash calls, the most Lists in
// flight at once, and can fail one folder's List.
type walkClient struct {
	driveclient.Client
	delay    time.Duration
	failList string

	mu       sync.Mutex
	ops      []string // "list <id>" / "trash <id>"
	inFlight int
	maxIn    int
}

func (c *walkClient) List(ctx context.Context, parentID string) ([]driveclient.File, error) {
	c.mu.Lock()
	c.ops = append(c.ops, "list "+parentID)
	c.inFlight++
	if c.inFlight > c.maxIn {
		c.maxIn = c.inFlight
	}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.inFlight--
		c.mu.Unlock()
	}()
	time.Sleep(c.delay)
	if parentID == c.failList {
		return nil, errors.New("transient listing failure")
	}
	return c.Client.List(ctx, parentID)
}

func (c *walkClient) Trash(ctx context.Context, fileID string) error {
	c.mu.Lock()
	c.ops = append(c.ops, "trash "+fileID)
	c.mu.Unlock()
	return c.Client.Trash(ctx, fileID)
}

func (c *walkClient) lists() int {
	n := 0
	for _, op := range c.ops {
		if strings.HasPrefix(op, "list ") {
			n++
		}
	}
	return n
}

// bigTree is sync/D with `fan` subfolders, each holding a file and `fan2`
// subfolders, each of those holding a file: 1 + fan + fan*fan2 folders.
type bigTree struct {
	fake     *driveclient.Fake
	sync, d  driveclient.File
	folders  []driveclient.File // every folder under D, D excluded
	deepest  driveclient.File   // one leaf folder
	deepFile driveclient.File   // the file inside it
	items    []reconcile.RemoteChild
}

func newBigTree(t *testing.T, fan, fan2 int) bigTree {
	t.Helper()
	ctx := context.Background()
	w := bigTree{fake: driveclient.NewFake()}
	must := func(f driveclient.File, err error) driveclient.File {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	add := func(f driveclient.File, parent string) driveclient.File {
		w.items = append(w.items, childOf(f, parent))
		return f
	}
	w.sync = must(w.fake.Mkdir(ctx, driveclient.FakeRootID, "sync"))
	w.d = must(w.fake.Mkdir(ctx, w.sync.ID, "D"))
	for i := 0; i < fan; i++ {
		s := add(must(w.fake.Mkdir(ctx, w.d.ID, fmt.Sprintf("s%d", i))), w.d.ID)
		w.folders = append(w.folders, s)
		add(must(w.fake.Upload(ctx, s.ID, "f.txt", strings.NewReader("x"), 1)), s.ID)
		for j := 0; j < fan2; j++ {
			ss := add(must(w.fake.Mkdir(ctx, s.ID, fmt.Sprintf("t%d", j))), s.ID)
			w.folders = append(w.folders, ss)
			w.deepFile = add(must(w.fake.Upload(ctx, ss.ID, "g.txt", strings.NewReader("y"), 1)), ss.ID)
			w.deepest = ss
		}
	}
	return w
}

func (w bigTree) trashD() reconcile.Action {
	return reconcile.Action{Type: reconcile.TrashRemote, RelPath: "D", FileID: w.d.ID, IsDir: true,
		RemoteWant: &reconcile.RemoteExpect{ParentID: w.sync.ID, Name: "D", IsDir: true,
			Contents: &reconcile.RemoteContents{Items: w.items}}}
}

// The trashed folder's own listing is the last List, after every other one
// has returned, and the Trash follows it directly.
func TestFolderTrashGateListsTheFolderItselfLast(t *testing.T) {
	w := newBigTree(t, 3, 3)
	c := &walkClient{Client: w.fake, delay: time.Millisecond}
	if err := (&Executor{Client: c}).gatedTrash(context.Background(), w.trashD()); err != nil {
		t.Fatalf("unchanged tree: %v", err)
	}
	n := len(c.ops)
	if n < 2 || c.ops[n-2] != "list "+w.d.ID || c.ops[n-1] != "trash "+w.d.ID {
		t.Fatalf("calls end with %v, want [list %s, trash %s]: the folder itself must be listed last", c.ops[max(0, n-3):], w.d.ID, w.d.ID)
	}
	for _, op := range c.ops[:n-2] {
		if op == "list "+w.d.ID {
			t.Errorf("the folder itself was listed before its subfolders: %v", c.ops)
		}
	}
}

// A few hundred folders: still exactly one List per folder, listed
// concurrently, and the unchanged tree is trashed.
func TestFolderTrashGateListsConcurrentlyOncePerFolder(t *testing.T) {
	w := newBigTree(t, 15, 20) // 1 + 15 + 300 folders
	c := &walkClient{Client: w.fake, delay: 200 * time.Microsecond}
	if err := (&Executor{Client: c}).gatedTrash(context.Background(), w.trashD()); err != nil {
		t.Fatalf("unchanged tree: %v", err)
	}
	if want := 1 + len(w.folders); c.lists() != want {
		t.Errorf("made %d List calls, want %d (one per folder)", c.lists(), want)
	}
	if c.maxIn < 2 {
		t.Errorf("at most %d List call in flight: the walk is serial", c.maxIn)
	}
	if d, _ := w.fake.Get(context.Background(), w.d.ID); !d.Trashed {
		t.Error("unchanged folder not trashed")
	}
}

// A listing that fails stops the walk: the action fails with that error (not
// as drift), the folder itself is never listed, nothing is trashed.
func TestFolderTrashGateListingErrorStopsTheWalk(t *testing.T) {
	w := newBigTree(t, 15, 20)
	c := &walkClient{Client: w.fake, failList: w.deepest.ID}
	err := (&Executor{Client: c}).gatedTrash(context.Background(), w.trashD())
	if err == nil || errors.Is(err, driveclient.ErrDrift) || !strings.Contains(err.Error(), "transient listing failure") {
		t.Fatalf("err = %v, want the listing failure, not drift", err)
	}
	for _, op := range c.ops {
		if op == "list "+w.d.ID || strings.HasPrefix(op, "trash ") {
			t.Errorf("after a failed listing the walk went on to %q: %v", op, c.ops)
		}
	}
}

// Serially (one worker) the subfolders go strictly deepest first, the folder
// itself last.
func TestFolderTrashGateSerialWalkIsDeepestFirst(t *testing.T) {
	withListWorkers(t, 1, func(t *testing.T) {
		w := newBigTree(t, 3, 2)
		depthOf := map[string]int{w.d.ID: 0}
		for _, it := range w.items {
			if it.IsDir {
				depthOf[it.FileID] = depthOf[it.ParentID] + 1
			}
		}
		c := &walkClient{Client: w.fake}
		if err := (&Executor{Client: c}).gatedTrash(context.Background(), w.trashD()); err != nil {
			t.Fatalf("unchanged tree: %v", err)
		}
		prev := 1 << 30
		for _, op := range c.ops {
			id, ok := strings.CutPrefix(op, "list ")
			if !ok {
				continue
			}
			if depthOf[id] > prev {
				t.Fatalf("listing order not deepest first: %v", c.ops)
			}
			prev = depthOf[id]
		}
	})
}

// On a few hundred folders, drift at every level refuses under the serial and
// the concurrent walk alike, and an unchanged tree passes under both.
func TestFolderTrashGateConcurrentMatchesSerialOnABigTree(t *testing.T) {
	bg := context.Background()
	drifts := []struct {
		name  string
		apply func(t *testing.T, w bigTree)
	}{
		{"none", func(*testing.T, bigTree) {}},
		{"deep file edited", func(t *testing.T, w bigTree) {
			if _, err := w.fake.Update(bg, w.deepFile.ID, strings.NewReader("theirs"), 6); err != nil {
				t.Fatal(err)
			}
		}},
		{"file added in a deep folder", func(t *testing.T, w bigTree) {
			if _, err := w.fake.Upload(bg, w.deepest.ID, "new.txt", strings.NewReader("x"), 1); err != nil {
				t.Fatal(err)
			}
		}},
		{"subfolder added mid-tree", func(t *testing.T, w bigTree) {
			if _, err := w.fake.Mkdir(bg, w.folders[0].ID, "new"); err != nil {
				t.Fatal(err)
			}
		}},
		{"file added at the top", func(t *testing.T, w bigTree) {
			if _, err := w.fake.Upload(bg, w.d.ID, "new.txt", strings.NewReader("x"), 1); err != nil {
				t.Fatal(err)
			}
		}},
		{"deep folder trashed", func(t *testing.T, w bigTree) {
			if err := w.fake.Trash(bg, w.deepest.ID); err != nil {
				t.Fatal(err)
			}
		}},
		{"deep file moved to another folder inside", func(t *testing.T, w bigTree) {
			if _, err := w.fake.Move(bg, w.deepFile.ID, w.folders[0].ID, "g.txt"); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, d := range drifts {
		t.Run(d.name, func(t *testing.T) {
			var verdicts []bool
			for _, workers := range []int{1, folderListWorkers} {
				withListWorkers(t, workers, func(t *testing.T) {
					w := newBigTree(t, 15, 20)
					d.apply(t, w)
					err := (&Executor{Client: w.fake}).gatedTrash(bg, w.trashD())
					if err != nil && !errors.Is(err, driveclient.ErrDrift) {
						t.Fatalf("workers=%d: %v", workers, err)
					}
					trashed, _ := w.fake.Get(bg, w.d.ID)
					if (err == nil) != trashed.Trashed {
						t.Fatalf("workers=%d: err %v but trashed=%v", workers, err, trashed.Trashed)
					}
					verdicts = append(verdicts, err == nil)
				})
			}
			if verdicts[0] != verdicts[1] {
				t.Errorf("serial passed=%v, concurrent passed=%v", verdicts[0], verdicts[1])
			}
			if want := d.name == "none"; verdicts[0] != want {
				t.Errorf("passed=%v, want %v", verdicts[0], want)
			}
		})
	}
}
