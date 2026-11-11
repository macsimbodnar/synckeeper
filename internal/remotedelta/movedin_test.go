package remotedelta

// S018: a folder moved or restored into the tree is walked after its batch;
// a walk stopped part-way — by error, or by process death — must be walked
// again, never leave the folder in the mirror with its contents missing.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// listFault fails List: the at-th call when at > 0, else every call naming
// failID until it has failed once. When crash is set it first takes the DB's
// crash image — the committed state a process killed at that instant leaves.
type listFault struct {
	driveclient.Client
	at, calls int
	failID    string
	failed    bool
	crash     func()
}

func (l *listFault) List(ctx context.Context, parentID string) ([]driveclient.File, error) {
	l.calls++
	if (l.at > 0 && l.calls == l.at) || (l.failID == parentID && !l.failed) {
		l.failed = true
		if l.crash != nil {
			l.crash()
		}
		return nil, errors.New("dial tcp: connection reset by peer")
	}
	return l.Client.List(ctx, parentID)
}

// crashImage copies the committed state of the DB at src to dst.
func crashImage(t *testing.T, src, dst string) {
	t.Helper()
	h, err := sql.Open("sqlite", src)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if _, err := h.Exec(`vacuum into ?`, dst); err != nil {
		t.Fatal(err)
	}
}

// openCache is newCache with the DB path returned, for the crash image.
func openCache(t *testing.T, fake *driveclient.Fake) (*statedb.DB, string, string) {
	t.Helper()
	ctx := context.Background()
	folder, err := fake.Mkdir(ctx, driveclient.FakeRootID, "Synckeeper")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := statedb.Open(path)
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
	return db, path, folder.ID
}

// reopen opens a crash image in place of the DB the run was using.
func reopen(t *testing.T, path string) *statedb.DB {
	t.Helper()
	db, err := statedb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func snapshotPaths(t *testing.T, db *statedb.DB, rootID string) []string {
	t.Helper()
	snap, _, err := Snapshot(db, rootID, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for p := range snap {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func mustUpload(t *testing.T, fake *driveclient.Fake, parent, name, content string) driveclient.File {
	t.Helper()
	f, err := fake.Upload(context.Background(), parent, name, strings.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func mustMkdir(t *testing.T, fake *driveclient.Fake, parent, name string) driveclient.File {
	t.Helper()
	f, err := fake.Mkdir(context.Background(), parent, name)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// The reviewer's reproduction (2026-11-11): `box` with `inner.txt` and
// `sub/deep.txt`, built out of the tree before the page token, moved into the
// root; List(box) fails once. Before the fix the snapshot stayed [box] for
// good — through two more refreshes and an edit of deep.txt on Drive.
func TestS018MovedInFolderWalkFailsOnce(t *testing.T) {
	for _, crash := range []bool{false, true} {
		t.Run(fmt.Sprintf("crash=%v", crash), func(t *testing.T) {
			ctx := context.Background()
			fake := driveclient.NewFake()
			box := mustMkdir(t, fake, driveclient.FakeRootID, "box")
			mustUpload(t, fake, box.ID, "inner.txt", "payload")
			sub := mustMkdir(t, fake, box.ID, "sub")
			deep := mustUpload(t, fake, sub.ID, "deep.txt", "deep v1")

			db, path, rootID := openCache(t, fake)
			refresh(t, fake, db, rootID)

			if _, err := fake.Move(ctx, box.ID, rootID, "box"); err != nil {
				t.Fatal(err)
			}
			image := filepath.Join(t.TempDir(), "crash.db")
			k := &listFault{Client: fake, failID: box.ID}
			if crash {
				k.crash = func() { crashImage(t, path, image) }
			}
			if err := Refresh(ctx, k, db, rootID); err == nil {
				t.Fatal("the refresh whose walk failed should have reported it")
			}
			if !k.failed {
				t.Fatal("setup: List(box) was never called")
			}
			if crash {
				db = reopen(t, image)
			}

			want := []string{"box", "box/inner.txt", "box/sub", "box/sub/deep.txt"}
			for i := 0; i < 2; i++ {
				refresh(t, fake, db, rootID)
				if got := snapshotPaths(t, db, rootID); strings.Join(got, ",") != strings.Join(want, ",") {
					t.Fatalf("refresh %d after the failed walk: snapshot = %v, want %v", i+1, got, want)
				}
			}

			if _, err := fake.Update(ctx, deep.ID, strings.NewReader("deep v2"), 7); err != nil {
				t.Fatal(err)
			}
			refresh(t, fake, db, rootID)
			snap, _, err := Snapshot(db, rootID, nil, false, false)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := fake.Get(ctx, deep.ID); err != nil || snap["box/sub/deep.txt"].MD5 != got.MD5 {
				t.Fatalf("deep.txt edit on Drive not in the mirror: %+v (err %v)", snap["box/sub/deep.txt"], err)
			}
		})
	}
}

// A multi-level moved-in folder whose walk stops at EVERY List — by error and
// by crash image: the refresh reports the failure, the snapshot never shows
// the folder without its contents, and the next refresh completes it.
func TestS018MovedInFolderInterruptedAtEveryList(t *testing.T) {
	ctx := context.Background()
	type world struct {
		fake         *driveclient.Fake
		db           *statedb.DB
		path, rootID string
		boxID        string
	}
	setup := func(t *testing.T) *world {
		t.Helper()
		fake := driveclient.NewFake()
		box := mustMkdir(t, fake, driveclient.FakeRootID, "box")
		mustUpload(t, fake, box.ID, "inner.txt", "inner")
		a := mustMkdir(t, fake, box.ID, "a")
		mustUpload(t, fake, a.ID, "a.txt", "a")
		deep := mustMkdir(t, fake, a.ID, "deep")
		mustUpload(t, fake, deep.ID, "deep.txt", "deep")
		mustMkdir(t, fake, deep.ID, "empty")
		b := mustMkdir(t, fake, box.ID, "b")
		mustUpload(t, fake, b.ID, "b.txt", "b")
		db, path, rootID := openCache(t, fake)
		mustUpload(t, fake, rootID, "top.txt", "top")
		refresh(t, fake, db, rootID)
		if _, err := fake.Move(ctx, box.ID, rootID, "box"); err != nil {
			t.Fatal(err)
		}
		return &world{fake: fake, db: db, path: path, rootID: rootID, boxID: box.ID}
	}
	want := strings.Join([]string{"box", "box/a", "box/a/a.txt", "box/a/deep", "box/a/deep/deep.txt",
		"box/a/deep/empty", "box/b", "box/b/b.txt", "box/inner.txt", "top.txt"}, ",")

	clean := setup(t)
	counter := &listFault{Client: clean.fake}
	if err := Refresh(ctx, counter, clean.db, clean.rootID); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(snapshotPaths(t, clean.db, clean.rootID), ","); got != want {
		t.Fatalf("clean refresh: snapshot = %s, want %s", got, want)
	}
	if counter.calls < 5 {
		t.Fatalf("a clean refresh made %d List calls; the moved-in tree has 5 folders", counter.calls)
	}

	for at := 1; at <= counter.calls; at++ {
		for _, crash := range []bool{false, true} {
			t.Run(fmt.Sprintf("list%d/crash=%v", at, crash), func(t *testing.T) {
				w := setup(t)
				k := &listFault{Client: w.fake, at: at}
				image := filepath.Join(t.TempDir(), "crash.db")
				if crash {
					k.crash = func() { crashImage(t, w.path, image) }
				}
				if err := Refresh(ctx, k, w.db, w.rootID); err == nil {
					t.Fatal("the interrupted refresh should have reported its error")
				}
				if crash {
					w.db = reopen(t, image)
				}
				// Whatever the interrupted walk left behind, the mirror must not
				// show the folder while its contents are not all in it.
				partial := snapshotPaths(t, w.db, w.rootID)
				for _, p := range partial {
					if strings.HasPrefix(p, "box") {
						t.Fatalf("after the interrupted walk the snapshot shows %v: box visible before its walk completed", partial)
					}
				}
				refresh(t, w.fake, w.db, w.rootID)
				if got := strings.Join(snapshotPaths(t, w.db, w.rootID), ","); got != want {
					t.Fatalf("refresh after the interrupted walk: snapshot = %s, want %s", got, want)
				}
			})
		}
	}
}

// A subfolder the interrupted walk had already recorded, moved straight into
// the root before the replay: its row exists but its contents were never
// listed. Row existence must not be read as "contents known".
func TestS018PartlyWalkedSubfolderMovedInDirectly(t *testing.T) {
	ctx := context.Background()
	fake := driveclient.NewFake()
	box := mustMkdir(t, fake, driveclient.FakeRootID, "box")
	sub := mustMkdir(t, fake, box.ID, "sub")
	mustUpload(t, fake, sub.ID, "deep.txt", "deep")
	db, rootID := newCache(t, fake)
	refresh(t, fake, db, rootID)

	if _, err := fake.Move(ctx, box.ID, rootID, "box"); err != nil {
		t.Fatal(err)
	}
	// List(box) succeeds and records sub's row; List(sub) fails.
	if err := Refresh(ctx, &listFault{Client: fake, failID: sub.ID}, db, rootID); err == nil {
		t.Fatal("the refresh whose walk failed should have reported it")
	}
	if !cachedIDs(t, db)[sub.ID] {
		t.Fatal("setup: the interrupted walk did not record sub's row")
	}
	if _, err := fake.Move(ctx, sub.ID, rootID, "sub"); err != nil {
		t.Fatal(err)
	}
	refresh(t, fake, db, rootID)
	want := "box,sub,sub/deep.txt"
	if got := strings.Join(snapshotPaths(t, db, rootID), ","); got != want {
		t.Fatalf("snapshot = %s, want %s", got, want)
	}
}

// One batch carrying the same folder twice — renamed outside the tree, then
// moved in (the fake's feed, like a paged Drive listing, need not coalesce):
// the first entry records an out-of-tree row; the second must still walk.
func TestS018FolderSeenOutsideThenMovedInSameBatch(t *testing.T) {
	ctx := context.Background()
	fake := driveclient.NewFake()
	box := mustMkdir(t, fake, driveclient.FakeRootID, "box")
	mustUpload(t, fake, box.ID, "inner.txt", "inner")
	db, rootID := newCache(t, fake)
	refresh(t, fake, db, rootID)

	if _, err := fake.Move(ctx, box.ID, driveclient.FakeRootID, "box2"); err != nil {
		t.Fatal(err)
	}
	if _, err := fake.Move(ctx, box.ID, rootID, "box"); err != nil {
		t.Fatal(err)
	}
	refresh(t, fake, db, rootID)
	want := "box,box/inner.txt"
	if got := strings.Join(snapshotPaths(t, db, rootID), ","); got != want {
		t.Fatalf("snapshot = %s, want %s", got, want)
	}
}

// A legacy trashed tombstone (rows written before trashed changes were
// dropped) links nothing into the tree: its folder, restored, is walked.
func TestS018RestoredFolderWithLegacyTombstoneIsWalked(t *testing.T) {
	ctx := context.Background()
	fake := driveclient.NewFake()
	box := mustMkdir(t, fake, driveclient.FakeRootID, "box")
	mustUpload(t, fake, box.ID, "inner.txt", "inner")
	db, rootID := newCache(t, fake)
	refresh(t, fake, db, rootID)
	if err := db.UpsertRemoteNode(statedb.RemoteNode{
		FileID: box.ID, ParentID: rootID, Name: "box",
		MimeType: driveclient.FolderMimeType, Trashed: true,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := fake.Move(ctx, box.ID, rootID, "box"); err != nil {
		t.Fatal(err)
	}
	refresh(t, fake, db, rootID)
	want := "box,box/inner.txt"
	if got := strings.Join(snapshotPaths(t, db, rootID), ","); got != want {
		t.Fatalf("snapshot = %s, want %s", got, want)
	}
}
