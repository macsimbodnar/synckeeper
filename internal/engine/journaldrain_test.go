package engine

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/executor"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// listFailer wraps the fake Drive and fails List for one folder id, with an
// error of the test's choosing. It is the only way to model "Drive keeps
// refusing this folder" for a reason that is not 404 — the fake has no
// permission model.
type listFailer struct {
	*driveclient.Fake
	folderID string
	err      error
	calls    atomic.Int32
}

func (l *listFailer) List(ctx context.Context, parentID string) ([]driveclient.File, error) {
	if parentID == l.folderID {
		l.calls.Add(1)
		return nil, l.err
	}
	return l.Fake.List(ctx, parentID)
}

// crashedCreateUnder leaves the state a crashed run leaves: one stale `upload`
// journal row under a tracked folder, with the file never recorded. It returns
// the folder's Drive id.
func crashedCreateUnder(t *testing.T, m *machine, dir string) string {
	t.Helper()
	m.write(t, dir+"/first.txt", "already synced")
	m.syncRaw(t)

	m.write(t, dir+"/orphan.txt", "created but never committed")
	var fired atomic.Bool
	executor.FaultHook = func(name string) error {
		if name == executor.CPUploadBeforeCommit && fired.CompareAndSwap(false, true) {
			return errors.New("injected crash at " + name)
		}
		return nil
	}
	defer func() { executor.FaultHook = nil }()
	if _, err := m.eng.Sync(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if !fired.Load() {
		t.Fatal("setup: the crash never fired, so there is no stale journal row to recover")
	}
	stale, err := m.db.StaleOps()
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) == 0 {
		t.Fatal("setup: no stale ops after the injected crash")
	}
	items, err := m.db.AllItems()
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.RelPath == dir {
			return it.DriveFileID
		}
	}
	t.Fatalf("setup: %s is not tracked", dir)
	return ""
}

func staleOpCount(t *testing.T, db *statedb.DB) int {
	t.Helper()
	stale, err := db.StaleOps()
	if err != nil {
		t.Fatal(err)
	}
	return len(stale)
}

// W18.14 — the review's F2, end to end. Crash recovery runs before the journal
// is cleared, so a stale create whose Drive parent it cannot inspect used to
// fail the cycle before anything else ran: nothing synced, on any path, for as
// long as the condition lasted (reproduced at 3/3 cycles, one op left).
//
// A parent that is GONE is the case that can be settled: whatever the crashed
// run may have created inside it went with it, so there is nothing to seed and
// nothing that could be duplicated — which is the only thing this recovery
// exists to prevent (W17).
func TestJournalDrainsWhenTheCrashedCreatesParentIsGone(t *testing.T) {
	fake, root := newWorld(t)
	m := newMachine(t, "a", fake, root)
	folderID := crashedCreateUnder(t, m, "docs")

	// The folder is purged from Drive while our journal still names it.
	fake.Forget(folderID)
	m.remove(t, "docs")
	m.write(t, "elsewhere.txt", "unrelated work")

	res, err := m.eng.Sync(context.Background(), Options{})
	if err != nil {
		t.Fatalf("one unrecoverable stale create still fails the whole cycle: %v", err)
	}
	if n := staleOpCount(t, m.db); n != 0 {
		t.Fatalf("journal still holds %d stale op(s); it must drain", n)
	}
	// And the rest of the machine is syncing — the actual damage of F2 was
	// never the stale row, it was everything else standing still behind it.
	if res.Failed > 0 {
		t.Fatalf("cycle had %d failed actions: %v", res.Failed, res.Errors)
	}
	if got := driveTreeOf(t, fake, root)["elsewhere.txt"]; got != "unrelated work" {
		t.Errorf("unrelated work did not reach Drive: %v", driveTreeOf(t, fake, root))
	}
	// Second cycle: settled, nothing left over.
	if n := staleOpCount(t, m.db); n != 0 {
		t.Fatalf("journal refilled: %d", n)
	}
	m.syncRaw(t)
}

// The other half, and the reason this is a classification rather than a blanket
// "ignore List failures": an error that may clear on its own must keep failing
// the cycle. Skipping it would let the replanned upload create a SECOND
// same-name item beside the one the crashed run already put on Drive — exactly
// the duplicate W17 exists to prevent, and Drive permits same-name siblings.
func TestATransientListFailureStillStopsTheCycle(t *testing.T) {
	fake, root := newWorld(t)
	m := newMachine(t, "a", fake, root)
	folderID := crashedCreateUnder(t, m, "docs")

	client := &listFailer{Fake: fake, folderID: folderID, err: errors.New("dial tcp: connection reset by peer")}
	m.eng.Client = client

	if _, err := m.eng.Sync(context.Background(), Options{}); err == nil {
		t.Fatal("a transient failure to inspect the parent must fail the cycle, not skip the op")
	}
	if n := staleOpCount(t, m.db); n != 1 {
		t.Fatalf("the stale op must survive a transient failure so recovery can retry it, got %d", n)
	}
	if client.calls.Load() == 0 {
		t.Fatal("the parent was never listed; this test is not exercising the path it claims")
	}

	// Once the condition clears, recovery does its W17 job properly: the
	// orphan is seeded and the replan adopts it instead of duplicating it.
	m.eng.Client = fake
	m.syncRaw(t)
	if n := staleOpCount(t, m.db); n != 0 {
		t.Fatalf("journal did not drain once the failure cleared: %d", n)
	}
	names := map[string]int{}
	for rel := range driveTreeOf(t, fake, root) {
		names[rel]++
	}
	if names["docs/orphan.txt"] != 1 {
		t.Errorf("Drive holds %d copies of docs/orphan.txt, want exactly 1: %v", names["docs/orphan.txt"], names)
	}
}
