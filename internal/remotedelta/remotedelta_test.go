package remotedelta

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// newCache returns a fake Drive with a sync root folder and a state DB whose
// page token is taken at call time (changes before it are never replayed).
// Two Drive siblings differing only by case collide on a case-insensitive
// target: the first by id is kept, the other skipped and reported; on a
// case-sensitive target both survive.
func TestSnapshotCaseCollision(t *testing.T) {
	ctx := context.Background()
	fake := driveclient.NewFake()
	db, rootID := newCache(t, fake)
	if _, err := fake.Upload(ctx, rootID, "a.txt", strings.NewReader("lower"), 5); err != nil {
		t.Fatal(err)
	}
	if _, err := fake.Upload(ctx, rootID, "A.txt", strings.NewReader("UPPER"), 5); err != nil {
		t.Fatal(err)
	}
	refresh(t, fake, db, rootID)

	// Case-sensitive: both present.
	snap, _, err := Snapshot(db, rootID, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap) != 2 {
		t.Fatalf("case-sensitive: want 2 items, got %d", len(snap))
	}

	// Case-insensitive: one kept (first by id = a.txt), one reported.
	snap, skips, err := Snapshot(db, rootID, nil, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap) != 1 {
		t.Fatalf("case-insensitive: want 1 item, got %d", len(snap))
	}
	if _, ok := snap["a.txt"]; !ok {
		t.Errorf("expected a.txt (first by id) to win; snapshot = %v", snap)
	}
	found := false
	for _, s := range skips {
		if strings.Contains(s.Reason, "case-collision") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a case-collision skip, got %v", skips)
	}
}

// N2 (spec §5, testing.md): two Drive siblings differing only by Unicode
// normalization (NFC vs NFD) collapse on a normalization-insensitive FS —
// first by id kept, the rest skipped and reported; both survive when the FS
// is normalization-sensitive.
func TestSnapshotNormalizationCollision(t *testing.T) {
	ctx := context.Background()
	fake := driveclient.NewFake()
	db, rootID := newCache(t, fake)
	nfcName := norm.NFC.String("café") + ".txt" // composed é
	nfdName := norm.NFD.String("café") + ".txt" // decomposed e + U+0301
	if nfcName == nfdName {
		t.Fatal("test setup: NFC and NFD names are identical")
	}
	if _, err := fake.Upload(ctx, rootID, nfcName, strings.NewReader("composed"), 8); err != nil {
		t.Fatal(err)
	}
	if _, err := fake.Upload(ctx, rootID, nfdName, strings.NewReader("decomposed!"), 10); err != nil {
		t.Fatal(err)
	}
	refresh(t, fake, db, rootID)

	// Normalization-sensitive: both present (distinct byte names).
	snap, _, err := Snapshot(db, rootID, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap) != 2 {
		t.Fatalf("normalization-sensitive: want 2 items, got %d", len(snap))
	}

	// Normalization-insensitive: one kept, one reported.
	snap, skips, err := Snapshot(db, rootID, nil, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap) != 1 {
		t.Fatalf("normalization-insensitive: want 1 item, got %d", len(snap))
	}
	found := false
	for _, s := range skips {
		if strings.Contains(s.Reason, "normalization-collision") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a normalization-collision skip, got %v", skips)
	}
}

func newCache(t *testing.T, fake *driveclient.Fake) (*statedb.DB, string) {
	t.Helper()
	ctx := context.Background()
	folder, err := fake.Mkdir(ctx, driveclient.FakeRootID, "Synckeeper")
	if err != nil {
		t.Fatal(err)
	}
	db, err := statedb.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	token, err := fake.StartPageToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetMeta(statedb.MetaPageToken, token); err != nil {
		t.Fatal(err)
	}
	return db, folder.ID
}

func refresh(t *testing.T, fake *driveclient.Fake, db *statedb.DB, rootID string) {
	t.Helper()
	if err := Refresh(context.Background(), fake, db, rootID); err != nil {
		t.Fatal(err)
	}
}

func cachedIDs(t *testing.T, db *statedb.DB) map[string]bool {
	t.Helper()
	nodes, err := db.AllRemoteNodes()
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, n := range nodes {
		ids[n.FileID] = true
	}
	return ids
}

// A trashed change removes the cache row instead of keeping a tombstone.
func TestTrashedChangeDropsRow(t *testing.T) {
	ctx := context.Background()
	fake := driveclient.NewFake()
	db, rootID := newCache(t, fake)

	f, err := fake.Upload(ctx, rootID, "a.txt", strings.NewReader("hello"), 5)
	if err != nil {
		t.Fatal(err)
	}
	refresh(t, fake, db, rootID)
	if !cachedIDs(t, db)[f.ID] {
		t.Fatal("expected a.txt cached after upload")
	}

	if err := fake.Trash(ctx, f.ID); err != nil {
		t.Fatal(err)
	}
	refresh(t, fake, db, rootID)
	if cachedIDs(t, db)[f.ID] {
		t.Fatal("trashed file still cached as a tombstone")
	}
}

// Trashing a folder orphans its cached children; prune removes them.
func TestPruneRemovesOrphanedChildren(t *testing.T) {
	ctx := context.Background()
	fake := driveclient.NewFake()
	db, rootID := newCache(t, fake)

	dir, err := fake.Mkdir(ctx, rootID, "docs")
	if err != nil {
		t.Fatal(err)
	}
	child, err := fake.Upload(ctx, dir.ID, "note.txt", strings.NewReader("x"), 1)
	if err != nil {
		t.Fatal(err)
	}
	refresh(t, fake, db, rootID)
	if ids := cachedIDs(t, db); !ids[dir.ID] || !ids[child.ID] {
		t.Fatal("expected folder and child cached")
	}

	// Only the folder gets a trash event; the child's row becomes unreachable.
	if err := fake.Trash(ctx, dir.ID); err != nil {
		t.Fatal(err)
	}
	refresh(t, fake, db, rootID)
	if ids := cachedIDs(t, db); ids[dir.ID] || ids[child.ID] {
		t.Fatalf("expected empty cache after folder trash, got %v", ids)
	}
}

// Out-of-tree changes are consumed (the feed is drive-wide) but pruned.
func TestPruneRemovesOutOfTreeRows(t *testing.T) {
	ctx := context.Background()
	fake := driveclient.NewFake()
	db, rootID := newCache(t, fake)

	other, err := fake.Mkdir(ctx, driveclient.FakeRootID, "Elsewhere")
	if err != nil {
		t.Fatal(err)
	}
	stray, err := fake.Upload(ctx, other.ID, "stray.txt", strings.NewReader("x"), 1)
	if err != nil {
		t.Fatal(err)
	}
	refresh(t, fake, db, rootID)
	if ids := cachedIDs(t, db); ids[other.ID] || ids[stray.ID] {
		t.Fatalf("out-of-tree rows survived prune: %v", ids)
	}
}

// A folder moved into the tree arrives as a single change event; its
// (pruned or never-cached) subtree must be repopulated by a walk.
func TestMoveInPopulatesSubtree(t *testing.T) {
	ctx := context.Background()
	fake := driveclient.NewFake()

	// Build the out-of-tree subtree BEFORE the cache's page token, so no
	// creation events are ever replayed for it.
	box, err := fake.Mkdir(ctx, driveclient.FakeRootID, "box")
	if err != nil {
		t.Fatal(err)
	}
	inner, err := fake.Upload(ctx, box.ID, "inner.txt", strings.NewReader("payload"), 7)
	if err != nil {
		t.Fatal(err)
	}

	db, rootID := newCache(t, fake)
	refresh(t, fake, db, rootID)
	if ids := cachedIDs(t, db); ids[box.ID] || ids[inner.ID] {
		t.Fatal("out-of-tree subtree unexpectedly cached")
	}

	if _, err := fake.Move(ctx, box.ID, rootID, "box"); err != nil {
		t.Fatal(err)
	}
	refresh(t, fake, db, rootID)
	snap, _, err := Snapshot(db, rootID, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap["box"]; !ok {
		t.Fatal("moved-in folder missing from snapshot")
	}
	if _, ok := snap["box/inner.txt"]; !ok {
		t.Fatal("moved-in folder's child missing from snapshot: subtree walk did not run")
	}
}

// R17 (A8): Snapshot must terminate on a cyclic parent chain in the cache.
// prune directly above carries a visited set; Snapshot's BFS did not, so a
// corrupted row that makes a folder its own ancestor — here the root listed
// as its own child, the one cycle reachable from the root under the
// single-parent schema — looped the daemon forever.
func TestR17SnapshotTerminatesOnParentCycle(t *testing.T) {
	fake := driveclient.NewFake()
	db, rootID := newCache(t, fake)
	if err := db.UpsertRemoteNode(statedb.RemoteNode{
		FileID: rootID, ParentID: rootID, Name: "loop",
		MimeType: driveclient.FolderMimeType,
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := Snapshot(db, rootID, nil, false, false)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Snapshot did not terminate: parent cycle in the cache")
	}
}

// N3's named case (re-scoped into W1.8.9): duplicate same-name siblings in
// one Drive folder collapse — first by id kept, the rest skipped and
// reported — independent of any filesystem folding.
func TestSnapshotDuplicateNameCollision(t *testing.T) {
	ctx := context.Background()
	fake := driveclient.NewFake()
	db, rootID := newCache(t, fake)
	first, err := fake.Upload(ctx, rootID, "dup.txt", strings.NewReader("first"), 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fake.Upload(ctx, rootID, "dup.txt", strings.NewReader("second"), 6); err != nil {
		t.Fatal(err)
	}
	refresh(t, fake, db, rootID)

	snap, skips, err := Snapshot(db, rootID, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap) != 1 {
		t.Fatalf("want 1 item after dedup, got %d: %v", len(snap), snap)
	}
	if got := snap["dup.txt"].FileID; got != first.ID {
		t.Errorf("kept id = %s, want first by id %s", got, first.ID)
	}
	found := false
	for _, s := range skips {
		if s.RelPath == "dup.txt" && strings.Contains(s.Reason, "duplicate name") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a duplicate-name skip for dup.txt, got %v", skips)
	}
}
