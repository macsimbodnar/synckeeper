package engine

// S010 (audit 2026-11-11 F04): the remote-write gate. Every Drive mutation
// the executor makes acts on the Drive state the cycle's plan was built on;
// another machine's write landing between this machine's refresh and its
// mutation must refuse the mutation, so the next cycle replans from fresh
// state and the decision table resolves it (conflict copy, edit beats
// delete). F04 itself — an UpdateRemote racing a remote edit — lives in
// audit_2026_11_11_test.go; these are the other mutations and drift kinds.

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
)

// raceClient stands in for another machine acting at the last moment before
// this machine's mutation reaches Drive — the same injection point F04's
// hookClient uses for Update, extended to Trash and Move.
type raceClient struct {
	driveclient.Client
	mu     sync.Mutex
	before func(op, fileID string)
}

func (r *raceClient) fire(op, fileID string) {
	r.mu.Lock()
	h := r.before
	r.mu.Unlock()
	if h != nil {
		h(op, fileID)
	}
}

func (r *raceClient) Update(ctx context.Context, fileID string, content io.Reader, size int64) (driveclient.File, error) {
	r.fire("update", fileID)
	return r.Client.Update(ctx, fileID, content, size)
}

func (r *raceClient) Trash(ctx context.Context, fileID string) error {
	r.fire("trash", fileID)
	return r.Client.Trash(ctx, fileID)
}

func (r *raceClient) Move(ctx context.Context, fileID, newParentID, newName string) (driveclient.File, error) {
	r.fire("move", fileID)
	return r.Client.Move(ctx, fileID, newParentID, newName)
}

// raceOnce runs one cycle on m with other's act injected right before m's
// first mutation of kind op, then hands m back its plain client. The cycle
// may report failed actions — a refused mutation is one — but must not
// error. It fails the test if the race never fired: a scenario that stopped
// planning the mutation would otherwise pass vacuously.
func raceOnce(t *testing.T, m *machine, fake *driveclient.Fake, op string, act func()) {
	t.Helper()
	raced := false
	m.eng.Client = &raceClient{Client: fake, before: func(got, _ string) {
		if got != op || raced {
			return
		}
		raced = true
		act()
	}}
	_, err := m.eng.Sync(context.Background(), Options{})
	m.eng.Client = fake
	if err != nil {
		t.Fatalf("[%s] racing sync: %v", m.name, err)
	}
	if !raced {
		t.Fatalf("[%s] the cycle never called %s; the scenario no longer exercises the race", m.name, op)
	}
}

// otherSync is one cycle on the other machine from inside a hook. Update
// runs in a transfer worker, so this reports with Errorf, never Fatalf.
func otherSync(t *testing.T, m *machine) {
	res, err := m.eng.Sync(context.Background(), Options{})
	if err != nil {
		t.Errorf("[%s] sync inside the race: %v", m.name, err)
		return
	}
	if res.Failed > 0 {
		t.Errorf("[%s] sync inside the race had %d failed actions: %v", m.name, res.Failed, res.Errors)
	}
}

func settle(t *testing.T, machines ...*machine) {
	t.Helper()
	for i := 0; i < 3; i++ {
		for _, m := range machines {
			m.sync(t)
		}
	}
}

// A TrashRemote racing a remote edit: A deleted the file, B edited it. Edit
// beats delete (§4.2) — the edit must survive on both machines, not be
// binned on Drive and then on B's disk.
func TestRemoteGateTrashRacingRemoteEdit(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "notes.txt", "v1")
	a.sync(t)
	b.sync(t)

	a.remove(t, "notes.txt")
	raceOnce(t, a, fake, "trash", func() {
		b.write(t, "notes.txt", "edit made on B")
		otherSync(t, b)
	})
	settle(t, a, b)

	for _, m := range []*machine{a, b} {
		if tree := m.listTree(t); tree["notes.txt"] != "edit made on B" {
			t.Errorf("[%s] B's edit did not survive A's concurrent delete: files on disk = %v", m.name, tree)
		}
	}
	assertConverged(t, a, b)
}

// A MoveRemote racing a remote edit: A renamed the file, B edited it under
// its old name. Both must survive. Without the gate the rename carried B's
// bytes to the new name on Drive while A's disk kept the old bytes there and
// recorded them as in sync: silent, permanent divergence.
func TestRemoteGateMoveRacingRemoteEdit(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "old.txt", "v1")
	a.sync(t)
	b.sync(t)

	a.rename(t, "old.txt", "new.txt")
	raceOnce(t, a, fake, "move", func() {
		b.write(t, "old.txt", "edit made on B")
		otherSync(t, b)
	})
	settle(t, a, b)

	for _, m := range []*machine{a, b} {
		tree := m.listTree(t)
		var all []string
		for _, c := range tree {
			all = append(all, c)
		}
		joined := strings.Join(all, "\n")
		if !strings.Contains(joined, "edit made on B") || !strings.Contains(joined, "v1") {
			t.Errorf("[%s] B's edit or A's renamed file is gone: files on disk = %v", m.name, tree)
		}
	}
	assertConverged(t, a, b)
}

// An UpdateRemote racing a remote trash: A edited the file, B deleted it.
// Edit beats delete: A's edit must be resurrected on Drive and reach B, not
// be written into a binned file and then binned on A's disk too.
func TestRemoteGateUpdateRacingRemoteTrash(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "notes.txt", "v1")
	a.sync(t)
	b.sync(t)

	a.write(t, "notes.txt", "edit made on A")
	raceOnce(t, a, fake, "update", func() {
		b.remove(t, "notes.txt")
		otherSync(t, b)
	})
	settle(t, a, b)

	for _, m := range []*machine{a, b} {
		if tree := m.listTree(t); tree["notes.txt"] != "edit made on A" {
			t.Errorf("[%s] A's edit did not survive B's concurrent delete: files on disk = %v", m.name, tree)
		}
	}
	assertConverged(t, a, b)
}

// A folder's MoveRemote racing a remote trash of that folder: A renamed the
// folder, B deleted it. A folder has no md5, so the folder gate pins name,
// parent and "not trashed" — this is the trashed arm. Without it the rename
// went through on a binned folder and A's next cycle binned A's own copy:
// the renamed content was gone from both machines.
func TestRemoteGateFolderMoveRacingRemoteTrash(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "docs/a.txt", "alpha")
	a.write(t, "docs/b.txt", "beta")
	a.sync(t)
	b.sync(t)

	a.rename(t, "docs", "papers")
	raceOnce(t, a, fake, "move", func() {
		b.remove(t, "docs")
		otherSync(t, b)
	})
	settle(t, a, b)

	for _, m := range []*machine{a, b} {
		tree := m.listTree(t)
		if tree["papers/a.txt"] != "alpha" || tree["papers/b.txt"] != "beta" {
			t.Errorf("[%s] A's renamed folder did not survive B's concurrent delete: files on disk = %v", m.name, tree)
		}
	}
	assertConverged(t, a, b)
}

// windowClient models the residual the gate leaves: another machine's write
// landing AFTER Drive passed the precondition and before the mutation. The
// fake checks and mutates atomically, so the test drops the precondition to
// stand in for a check that already passed.
type windowClient struct {
	driveclient.Client
	beforeMove func()
}

func (w *windowClient) Move(_ context.Context, fileID, newParentID, newName string) (driveclient.File, error) {
	if h := w.beforeMove; h != nil {
		w.beforeMove = nil
		h()
	}
	return w.Client.Move(context.Background(), fileID, newParentID, newName)
}

// A remote edit inside the gate's window rides along with the move; Drive's
// reply shows it. The commit must not record that content as what the local
// file holds, or A keeps the old bytes under the new name forever while
// every other machine has B's edit (silent divergence, the R18 class).
func TestRemoteGateMoveWindowEditStillConverges(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "old.txt", "v1")
	a.sync(t)
	b.sync(t)

	a.rename(t, "old.txt", "new.txt")
	raced := false
	a.eng.Client = &windowClient{Client: fake, beforeMove: func() {
		raced = true
		b.write(t, "old.txt", "edit made on B")
		otherSync(t, b)
	}}
	_, err := a.eng.Sync(context.Background(), Options{})
	a.eng.Client = fake
	if err != nil {
		t.Fatalf("[a] racing sync: %v", err)
	}
	if !raced {
		t.Fatal("[a] the cycle never moved old.txt; the scenario no longer exercises the window")
	}
	settle(t, a, b)

	for _, m := range []*machine{a, b} {
		if tree := m.listTree(t); tree["new.txt"] != "edit made on B" {
			t.Errorf("[%s] B's edit, carried to new.txt by A's rename, is not what this machine holds: files on disk = %v", m.name, tree)
		}
	}
	assertConverged(t, a, b)
}
