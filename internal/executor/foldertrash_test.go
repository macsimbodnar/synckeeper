package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// S021: the folder-trash gate's verdicts on the fake, one per kind of drift
// inside the folder. World: sync/D holds a.txt and s/, s holds b.txt; the
// folder trash expects exactly that.

type folderWorld struct {
	fake          *driveclient.Fake
	sync, d, s    driveclient.File
	a, b, outside driveclient.File
}

func newFolderWorld(t *testing.T) folderWorld {
	t.Helper()
	ctx := context.Background()
	w := folderWorld{fake: driveclient.NewFake()}
	must := func(f driveclient.File, err error) driveclient.File {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	w.sync = must(w.fake.Mkdir(ctx, driveclient.FakeRootID, "sync"))
	w.d = must(w.fake.Mkdir(ctx, w.sync.ID, "D"))
	w.outside = must(w.fake.Mkdir(ctx, w.sync.ID, "elsewhere"))
	w.a = must(w.fake.Upload(ctx, w.d.ID, "a.txt", strings.NewReader("alpha"), 5))
	w.s = must(w.fake.Mkdir(ctx, w.d.ID, "s"))
	w.b = must(w.fake.Upload(ctx, w.s.ID, "b.txt", strings.NewReader("beta"), 4))
	return w
}

func childOf(f driveclient.File, parent string) reconcile.RemoteChild {
	return reconcile.RemoteChild{FileID: f.ID, ParentID: parent, Name: f.Name, IsDir: f.IsDir(), MD5: f.MD5}
}

// trashD is the folder trash as the planner and PinFolderTrashContents build it.
func (w folderWorld) trashD(items ...reconcile.RemoteChild) reconcile.Action {
	return reconcile.Action{Type: reconcile.TrashRemote, RelPath: "D", FileID: w.d.ID, IsDir: true,
		RemoteWant: &reconcile.RemoteExpect{ParentID: w.sync.ID, Name: "D", IsDir: true,
			Contents: &reconcile.RemoteContents{Items: items}}}
}

func (w folderWorld) full() reconcile.Action {
	return w.trashD(childOf(w.a, w.d.ID), childOf(w.s, w.d.ID), childOf(w.b, w.s.ID))
}

type folderDrift struct {
	name  string
	apply func(t *testing.T, w folderWorld)
}

func folderDrifts() []folderDrift {
	bg := context.Background()
	return []folderDrift{
		{"file edited", func(t *testing.T, w folderWorld) {
			if _, err := w.fake.Update(bg, w.a.ID, strings.NewReader("theirs"), 6); err != nil {
				t.Fatal(err)
			}
		}},
		{"file in a subfolder edited", func(t *testing.T, w folderWorld) {
			if _, err := w.fake.Update(bg, w.b.ID, strings.NewReader("theirs"), 6); err != nil {
				t.Fatal(err)
			}
		}},
		{"file added", func(t *testing.T, w folderWorld) {
			if _, err := w.fake.Upload(bg, w.d.ID, "new.txt", strings.NewReader("x"), 1); err != nil {
				t.Fatal(err)
			}
		}},
		{"file added in a subfolder", func(t *testing.T, w folderWorld) {
			if _, err := w.fake.Upload(bg, w.s.ID, "new.txt", strings.NewReader("x"), 1); err != nil {
				t.Fatal(err)
			}
		}},
		{"subfolder added", func(t *testing.T, w folderWorld) {
			if _, err := w.fake.Mkdir(bg, w.d.ID, "sub"); err != nil {
				t.Fatal(err)
			}
		}},
		{"file renamed", func(t *testing.T, w folderWorld) {
			if _, err := w.fake.Move(bg, w.a.ID, w.d.ID, "renamed.txt"); err != nil {
				t.Fatal(err)
			}
		}},
		{"file moved between folders inside", func(t *testing.T, w folderWorld) {
			if _, err := w.fake.Move(bg, w.a.ID, w.s.ID, "a.txt"); err != nil {
				t.Fatal(err)
			}
		}},
		{"file moved out", func(t *testing.T, w folderWorld) {
			if _, err := w.fake.Move(bg, w.a.ID, w.outside.ID, "a.txt"); err != nil {
				t.Fatal(err)
			}
		}},
		{"file trashed", func(t *testing.T, w folderWorld) {
			if err := w.fake.Trash(bg, w.b.ID); err != nil {
				t.Fatal(err)
			}
		}},
		{"subfolder trashed", func(t *testing.T, w folderWorld) {
			if err := w.fake.Trash(bg, w.s.ID); err != nil {
				t.Fatal(err)
			}
		}},
		{"file gone", func(t *testing.T, w folderWorld) { w.fake.Forget(w.a.ID) }},
	}
}

// withListWorkers runs fn with the gate's listing concurrency set to n.
func withListWorkers(t *testing.T, n int, fn func(t *testing.T)) {
	t.Helper()
	old := folderListWorkers
	folderListWorkers = n
	defer func() { folderListWorkers = old }()
	fn(t)
}

// Every drift kind refuses, under the serial walk and the concurrent one
// alike (round 2: the walk became concurrent; the verdicts must not change).
func TestFolderTrashGateRefusesDriftInside(t *testing.T) {
	bg := context.Background()
	for _, workers := range []int{1, folderListWorkers} {
		for _, tc := range folderDrifts() {
			t.Run(fmt.Sprintf("workers=%d/%s", workers, tc.name), func(t *testing.T) {
				withListWorkers(t, workers, func(t *testing.T) {
					w := newFolderWorld(t)
					tc.apply(t, w)
					trashes := w.fake.TrashCount()
					x := &Executor{Client: w.fake}
					err := x.gatedTrash(bg, w.full())
					if !errors.Is(err, driveclient.ErrDrift) {
						t.Fatalf("folder trash after %q: err = %v, want ErrDrift", tc.name, err)
					}
					if w.fake.TrashCount() != trashes {
						t.Error("refused folder trash still sent a trash")
					}
					if d, _ := w.fake.Get(bg, w.d.ID); d.Trashed {
						t.Error("refused folder trash binned the folder")
					}
				})
			})
		}
	}
}

func TestFolderTrashGatePassesWhenContentsMatch(t *testing.T) {
	bg := context.Background()
	t.Run("collapsed: the whole subtree as planned", func(t *testing.T) {
		w := newFolderWorld(t)
		if err := (&Executor{Client: w.fake}).gatedTrash(bg, w.full()); err != nil {
			t.Fatalf("unchanged folder: %v", err)
		}
		if d, _ := w.fake.Get(bg, w.d.ID); !d.Trashed {
			t.Error("folder not trashed")
		}
	})
	t.Run("uncollapsed: emptied by the plan, expected empty", func(t *testing.T) {
		w := newFolderWorld(t)
		for _, id := range []string{w.b.ID, w.s.ID, w.a.ID} {
			if err := w.fake.Trash(bg, id); err != nil {
				t.Fatal(err)
			}
		}
		if err := (&Executor{Client: w.fake}).gatedTrash(bg, w.trashD()); err != nil {
			t.Fatalf("emptied folder: %v", err)
		}
	})
	t.Run("uncollapsed: expected empty, an item left behind", func(t *testing.T) {
		w := newFolderWorld(t)
		for _, id := range []string{w.b.ID, w.s.ID} { // a.txt's own trash "failed"
			if err := w.fake.Trash(bg, id); err != nil {
				t.Fatal(err)
			}
		}
		if err := (&Executor{Client: w.fake}).gatedTrash(bg, w.trashD()); !errors.Is(err, driveclient.ErrDrift) {
			t.Fatalf("err = %v, want ErrDrift: the folder would take a.txt along", err)
		}
	})
	t.Run("contents not pinned: unchecked, as before S021", func(t *testing.T) {
		w := newFolderWorld(t)
		if _, err := w.fake.Upload(bg, w.d.ID, "new.txt", strings.NewReader("x"), 1); err != nil {
			t.Fatal(err)
		}
		a := w.full()
		a.RemoteWant.Contents = nil
		if err := (&Executor{Client: w.fake}).gatedTrash(bg, a); err != nil {
			t.Fatalf("unpinned folder trash: %v", err)
		}
	})
}
