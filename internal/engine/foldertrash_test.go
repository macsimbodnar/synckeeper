package engine

// S021: a folder trash looks inside the folder. Trashing a Drive folder takes
// everything in it to Drive's bin, so the remote-write gate (S010) must refuse
// the trash when the folder's contents on Drive are no longer what the plan
// saw: a file edited or added inside by another machine after this machine's
// refresh would otherwise go to the bin with the folder and then off the other
// machine's disk. Both folder-trash shapes are covered: the collapsed one (one
// TrashRemote standing for the whole subtree) and the uncollapsed one
// (per-item trashes, then the folder) that a machine with no system bin plans.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
)

// gateRaceClient injects another machine's write at the first call matching
// match, and counts the List and Trash calls the cycle makes. Lists are where
// the folder-trash gate reads Drive; a race matched on the folder's own
// listing or trash lands after this machine's refresh and before its folder
// trash is checked.
type gateRaceClient struct {
	driveclient.Client
	mu     sync.Mutex
	match  func(op, fileID string) bool
	act    func()
	fired  bool
	lists  int
	trashs int
}

func (c *gateRaceClient) hit(op, fileID string) {
	c.mu.Lock()
	if op == "list" {
		c.lists++
	} else {
		c.trashs++
	}
	fire := !c.fired && c.match != nil && c.match(op, fileID)
	if fire {
		c.fired = true
	}
	c.mu.Unlock()
	if fire {
		c.act()
	}
}

func (c *gateRaceClient) List(ctx context.Context, parentID string) ([]driveclient.File, error) {
	c.hit("list", parentID)
	return c.Client.List(ctx, parentID)
}

func (c *gateRaceClient) Trash(ctx context.Context, fileID string) error {
	c.hit("trash", fileID)
	return c.Client.Trash(ctx, fileID)
}

// atFolder matches the start of the folder trash: the gate's listing of the
// folder, or the Trash itself where nothing lists it first.
func atFolder(id string) func(op, fileID string) bool {
	return func(_ string, fileID string) bool { return fileID == id }
}

// atFirstTrash matches the cycle's first Trash call, whatever it trashes.
func atFirstTrash(op, _ string) bool { return op == "trash" }

// raceFolderTrash runs one cycle on m through a gateRaceClient and hands m
// back its plain client. The cycle may report failed actions (a refused trash
// is one) but must not error, and the race must have fired.
func raceFolderTrash(t *testing.T, m *machine, fake *driveclient.Fake, match func(op, fileID string) bool, act func()) (*Result, *gateRaceClient) {
	t.Helper()
	c := &gateRaceClient{Client: fake, match: match, act: act}
	m.eng.Client = c
	res, err := m.eng.Sync(context.Background(), Options{})
	m.eng.Client = fake
	if err != nil {
		t.Fatalf("[%s] racing sync: %v", m.name, err)
	}
	if act != nil && !c.fired {
		t.Fatalf("[%s] the race never fired; the scenario no longer exercises the folder trash", m.name)
	}
	return res, c
}

// docsWorld is two machines in sync on docs/a.txt and docs/b.txt.
func docsWorld(t *testing.T) (*driveclient.Fake, *machine, *machine) {
	t.Helper()
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "docs/a.txt", "alpha")
	a.write(t, "docs/b.txt", "beta")
	a.sync(t)
	b.sync(t)
	return fake, a, b
}

func (m *machine) mkdir(t *testing.T, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(m.dir, filepath.FromSlash(rel)), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (m *machine) isDir(rel string) bool {
	fi, err := os.Stat(filepath.Join(m.dir, filepath.FromSlash(rel)))
	return err == nil && fi.IsDir()
}

// refusedAsDrift asserts the racing cycle failed exactly one action, the
// folder trash, as a gate refusal.
func refusedAsDrift(t *testing.T, res *Result) {
	t.Helper()
	if res.Failed != 1 || len(res.Errors) != 1 ||
		!strings.Contains(res.Errors[0], "trash_remote docs:") || !strings.Contains(res.Errors[0], driveclient.ErrDrift.Error()) {
		t.Fatalf("racing cycle: failed %d, errors %v; want the docs trash refused as drift", res.Failed, res.Errors)
	}
}

// Collapsed shape, edit: A deletes docs (one TrashRemote for the folder), B
// edits docs/a.txt after A's refresh. Edit beats delete: B's edit must stay on
// both machines, not ride to Drive's bin with the folder.
func TestS021CollapsedFolderTrashRacingEditInside(t *testing.T) {
	fake, a, b := docsWorld(t)
	folder := driveIDOf(t, a, "docs")

	a.remove(t, "docs")
	raceFolderTrash(t, a, fake, atFolder(folder), func() {
		b.write(t, "docs/a.txt", "edit made on B")
		otherSync(t, b)
	})
	settle(t, a, b)

	for _, m := range []*machine{a, b} {
		if tree := m.listTree(t); tree["docs/a.txt"] != "edit made on B" {
			t.Errorf("[%s] B's edit inside the folder A deleted did not survive: files on disk = %v", m.name, tree)
		}
	}
	assertConverged(t, a, b)
}

// Collapsed shape, add: B adds docs/new.txt after A's refresh.
func TestS021CollapsedFolderTrashRacingAddInside(t *testing.T) {
	fake, a, b := docsWorld(t)
	folder := driveIDOf(t, a, "docs")

	a.remove(t, "docs")
	raceFolderTrash(t, a, fake, atFolder(folder), func() {
		b.write(t, "docs/new.txt", "added on B")
		otherSync(t, b)
	})
	settle(t, a, b)

	for _, m := range []*machine{a, b} {
		if tree := m.listTree(t); tree["docs/new.txt"] != "added on B" {
			t.Errorf("[%s] B's file added inside the folder A deleted did not survive: files on disk = %v", m.name, tree)
		}
	}
	assertConverged(t, a, b)
}

// Uncollapsed shape, edit: A has no system bin, so the deletion is planned
// item by item — trash docs/b.txt, trash docs/a.txt, then trash docs. B edits
// docs/b.txt just before A's first trash: that trash is refused by S010's
// per-file gate, and the folder trash must not then take the file anyway.
func TestS021UncollapsedFolderTrashRacingEditInside(t *testing.T) {
	fake, a, b := docsWorld(t)
	a.bin.Unavailable = true

	a.remove(t, "docs")
	raceFolderTrash(t, a, fake, atFirstTrash, func() {
		b.write(t, "docs/b.txt", "edit made on B")
		otherSync(t, b)
	})
	settle(t, a, b)

	for _, m := range []*machine{a, b} {
		if tree := m.listTree(t); tree["docs/b.txt"] != "edit made on B" {
			t.Errorf("[%s] B's edit inside the folder A deleted did not survive: files on disk = %v", m.name, tree)
		}
	}
	assertConverged(t, a, b)
}

// Uncollapsed shape, add: A's per-file trashes have run; B adds docs/new.txt
// before A trashes the then-supposedly-empty folder.
func TestS021UncollapsedFolderTrashRacingAddInside(t *testing.T) {
	fake, a, b := docsWorld(t)
	a.bin.Unavailable = true
	folder := driveIDOf(t, a, "docs")

	a.remove(t, "docs")
	raceFolderTrash(t, a, fake, atFolder(folder), func() {
		b.write(t, "docs/new.txt", "added on B")
		otherSync(t, b)
	})
	settle(t, a, b)

	for _, m := range []*machine{a, b} {
		if tree := m.listTree(t); tree["docs/new.txt"] != "added on B" {
			t.Errorf("[%s] B's file added inside the folder A deleted did not survive: files on disk = %v", m.name, tree)
		}
	}
	assertConverged(t, a, b)
}

// A subfolder added inside, empty: a folder-only change the walk must see.
func TestS021FolderTrashRacingSubfolderAdded(t *testing.T) {
	fake, a, b := docsWorld(t)
	folder := driveIDOf(t, a, "docs")

	a.remove(t, "docs")
	res, _ := raceFolderTrash(t, a, fake, atFolder(folder), func() {
		b.mkdir(t, "docs/sub")
		otherSync(t, b)
	})
	refusedAsDrift(t, res)
	settle(t, a, b)

	for _, m := range []*machine{a, b} {
		if !m.isDir("docs/sub") {
			t.Errorf("[%s] B's subfolder added inside the folder A deleted is gone", m.name)
		}
	}
	assertConverged(t, a, b)
}

// A file inside trashed by the other machine: nothing to lose, but the plan's
// picture of the folder is stale — refused; the next cycle forgets the file
// and trashes what is left.
func TestS021FolderTrashRacingFileTrashedInside(t *testing.T) {
	fake, a, b := docsWorld(t)
	folder := driveIDOf(t, a, "docs")

	a.remove(t, "docs")
	res, _ := raceFolderTrash(t, a, fake, atFolder(folder), func() {
		b.remove(t, "docs/a.txt")
		otherSync(t, b)
	})
	refusedAsDrift(t, res)
	settle(t, a, b)

	for _, m := range []*machine{a, b} {
		if m.exists("docs") {
			t.Errorf("[%s] docs survived although both its files were deleted: files on disk = %v", m.name, m.listTree(t))
		}
	}
	assertConverged(t, a, b)
}

// A file inside moved out of the folder by the other machine: refused; the
// next cycle downloads the moved file to A (edit-beats-delete's "moved" row)
// and trashes the folder.
func TestS021FolderTrashRacingFileMovedOut(t *testing.T) {
	fake, a, b := docsWorld(t)
	folder := driveIDOf(t, a, "docs")

	a.remove(t, "docs")
	res, _ := raceFolderTrash(t, a, fake, atFolder(folder), func() {
		b.rename(t, "docs/a.txt", "a.txt")
		otherSync(t, b)
	})
	refusedAsDrift(t, res)
	settle(t, a, b)

	for _, m := range []*machine{a, b} {
		tree := m.listTree(t)
		if tree["a.txt"] != "alpha" || m.exists("docs") {
			t.Errorf("[%s] want a.txt kept and docs gone: files on disk = %v, docs exists %v", m.name, tree, m.exists("docs"))
		}
	}
	assertConverged(t, a, b)
}

// The no-race control, both shapes: a plain folder delete still propagates,
// and the gate costs exactly one List per folder it checks — collapsed, the
// folder and each subfolder (one Trash call in all); uncollapsed, one per
// folder trash, each expecting its folder empty.
func TestS021PlainFolderDeleteCostsOneListPerFolder(t *testing.T) {
	for _, tc := range []struct {
		name               string
		noBin              bool
		wantLists, wantTrs int
	}{
		{"collapsed", false, 4, 1},
		{"uncollapsed", true, 4, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake, root := newWorld(t)
			a := newMachine(t, "a", fake, root)
			b := newMachine(t, "b", fake, root)
			a.write(t, "docs/a.txt", "alpha")
			a.write(t, "docs/s1/b.txt", "beta")
			a.write(t, "docs/s1/s2/c.txt", "gamma")
			a.write(t, "docs/s3/d.txt", "delta")
			a.write(t, "keep.txt", "kept")
			a.sync(t)
			b.sync(t)
			a.bin.Unavailable = tc.noBin

			a.remove(t, "docs")
			res, c := raceFolderTrash(t, a, fake, nil, nil)
			if res.Failed != 0 {
				t.Fatalf("plain folder delete failed %d actions: %v", res.Failed, res.Errors)
			}
			if c.lists != tc.wantLists || c.trashs != tc.wantTrs {
				t.Errorf("cycle made %d List and %d Trash calls, want %d and %d", c.lists, c.trashs, tc.wantLists, tc.wantTrs)
			}
			settle(t, a, b)
			for _, m := range []*machine{a, b} {
				if m.exists("docs") {
					t.Errorf("[%s] docs survived its deletion", m.name)
				}
			}
			assertConverged(t, a, b)
		})
	}
}

// The residual S021 leaves (spec §7): a write landing after the gate's last
// List and before the Trash lands is not seen, and goes to Drive's bin with
// the folder — then off B's disk. Red, skipped; it pins the window. On the
// fake the window is the gap between the walk and the Trash call (the client's
// own check of the folder and its trash are atomic there).
func TestS021WriteAfterTheListingIsTheResidual(t *testing.T) {
	t.Skip("known residual of S021: a write between the folder-trash gate's listing and the trash; see the comment")
	fake, a, b := docsWorld(t)
	folder := driveIDOf(t, a, "docs")

	a.remove(t, "docs")
	raceFolderTrash(t, a, fake, func(op, id string) bool { return op == "trash" && id == folder }, func() {
		b.write(t, "docs/a.txt", "edit made on B")
		otherSync(t, b)
	})
	settle(t, a, b)

	for _, m := range []*machine{a, b} {
		if tree := m.listTree(t); tree["docs/a.txt"] != "edit made on B" {
			t.Errorf("[%s] B's edit landing inside the window went to Drive's bin with the folder: files on disk = %v", m.name, tree)
		}
	}
}
