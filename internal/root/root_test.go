package root

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/remotedelta"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

func openDB(t *testing.T) *statedb.DB {
	t.Helper()
	db, err := statedb.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// world builds a fake Drive with a "Synckeeper" folder holding one file, and a
// DB already synced against it: the ordinary state of a working machine.
func world(t *testing.T) (*driveclient.Fake, *statedb.DB, driveclient.File) {
	t.Helper()
	ctx := context.Background()
	fake := driveclient.NewFake()
	folder, err := fake.Mkdir(ctx, driveclient.FakeRootID, "Synckeeper")
	if err != nil {
		t.Fatal(err)
	}
	f, err := fake.Upload(ctx, folder.ID, "a.txt", strings.NewReader("hello"), 5)
	if err != nil {
		t.Fatal(err)
	}
	db := openDB(t)
	if err := db.SetMeta(statedb.MetaRootFolderID, folder.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.SetMeta(statedb.MetaRootFolderName, "Synckeeper"); err != nil {
		t.Fatal(err)
	}
	if err := db.Tx(func(tx *sql.Tx) error {
		return statedb.UpsertItem(tx, statedb.Item{
			DriveFileID: f.ID, RelPath: "a.txt", Size: 5, ContentMD5: f.MD5, DriveMD5: f.MD5, DriveVersion: f.Version,
		})
	}); err != nil {
		t.Fatal(err)
	}
	return fake, db, folder
}

// W18.3 — the review's F1, at its root. A folder renamed in the Drive web UI
// must keep its identity: same id, baseline untouched, no new folder minted.
// Before W18-A this resolved by NAME, found nothing called "Synckeeper",
// created an empty one, repointed at it and left every baseline row — after
// which the next ordinary cycle binned the user's whole tree.
func TestRenamedDriveFolderKeepsItsIdentity(t *testing.T) {
	ctx := context.Background()
	fake, db, folder := world(t)

	if _, err := fake.Move(ctx, folder.ID, driveclient.FakeRootID, "Synckeeper-old"); err != nil {
		t.Fatal(err)
	}

	res, err := Resolve(ctx, fake, db, "Synckeeper")
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != folder.ID {
		t.Fatalf("resolved to %s, want the existing folder %s — F1 is back", res.ID, folder.ID)
	}
	if res.Created {
		t.Error("a new folder was created for a folder that merely got renamed")
	}
	if !res.Renamed {
		t.Error("the rename was not noticed")
	}
	if res.Name != "Synckeeper-old" {
		t.Errorf("name = %q, want the observed Synckeeper-old", res.Name)
	}
	if got := Name(db, "Synckeeper"); got != "Synckeeper-old" {
		t.Errorf("recorded name = %q, want Synckeeper-old — the views must not show a stale name", got)
	}
	// The baseline is what F1 destroyed: it must be entirely untouched.
	items, err := db.AllItems()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].RelPath != "a.txt" {
		t.Fatalf("baseline = %+v, want the single a.txt row intact", items)
	}
}

// A folder that is really gone is recreated — and the baseline is reset with
// it, so the coming cycle uploads instead of deleting (spec §11, W18-C).
func TestGoneDriveFolderIsRecreatedAndTheBaselineReset(t *testing.T) {
	ctx := context.Background()
	fake, db, folder := world(t)
	fake.Forget(folder.ID)

	res, err := Resolve(ctx, fake, db, "Synckeeper")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.ID == folder.ID {
		t.Fatalf("res = %+v, want a freshly created folder", res)
	}
	if res.Name != "Synckeeper" {
		t.Errorf("new folder name = %q, want the configured Synckeeper", res.Name)
	}
	items, err := db.AllItems()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("baseline = %d rows, want 0 — without the reset the next cycle reads them as deleted on Drive", len(items))
	}
	if id, _ := db.GetMeta(statedb.MetaRootFolderID); id != res.ID {
		t.Errorf("stored root id = %q, want the new %s", id, res.ID)
	}
	// The mirror must describe the new folder, not the old one.
	if _, err := db.GetMeta(remotedelta.MetaWalkDone); err != nil {
		t.Errorf("walk marker missing after create: %v — the mirror was not rebuilt", err)
	}
}

// A folder moved to Drive's bin counts as gone: it cannot be synced into, and
// per W18 a deleted root is recreated rather than propagated as a deletion.
func TestBinnedDriveFolderIsTreatedAsGone(t *testing.T) {
	ctx := context.Background()
	fake, db, folder := world(t)
	if err := fake.Trash(ctx, folder.ID); err != nil {
		t.Fatal(err)
	}

	res, err := Resolve(ctx, fake, db, "Synckeeper")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.ID == folder.ID {
		t.Fatalf("res = %+v, want a freshly created folder for a binned root", res)
	}
	if n, _ := db.ItemCount(); n != 0 {
		t.Errorf("baseline = %d rows, want 0", n)
	}
}

// The distinction that keeps the whole thing safe: a transient failure must
// never look like deletion. Misreading one would recreate the folder and
// re-upload the entire tree against a folder that was there all along.
func TestTransientFailureIsNotTreatedAsDeletion(t *testing.T) {
	ctx := context.Background()
	_, db, folder := world(t)

	boom := errors.New("dial tcp: connection refused")
	client := &failingGet{err: boom}

	if _, err := Resolve(ctx, client, db, "Synckeeper"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the transient failure surfaced", err)
	}
	if id, _ := db.GetMeta(statedb.MetaRootFolderID); id != folder.ID {
		t.Errorf("root id = %q, want it left alone at %s", id, folder.ID)
	}
	if n, _ := db.ItemCount(); n != 1 {
		t.Errorf("baseline = %d rows, want the original 1 — a network blip must not reset anything", n)
	}
}

// A DB that lost its metadata resolves by name — the only case that may — and
// resets its now-unmoored baseline so the union merge rebuilds it. This is
// what makes an idempotent `init` a complete recovery for a lost DB.
func TestLostRootIDResolvesByNameAndResetsTheBaseline(t *testing.T) {
	ctx := context.Background()
	fake, db, folder := world(t)
	if err := db.Tx(func(tx *sql.Tx) error {
		return statedb.DeleteMetaTx(tx, statedb.MetaRootFolderID)
	}); err != nil {
		t.Fatal(err)
	}

	res, err := Resolve(ctx, fake, db, "Synckeeper")
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != folder.ID {
		t.Fatalf("resolved to %s, want the existing folder %s found by name", res.ID, folder.ID)
	}
	if n, _ := db.ItemCount(); n != 0 {
		t.Errorf("baseline = %d rows, want 0 — rows built against an unnameable root cannot be trusted", n)
	}
}

// failingGet answers Get with a transient error and nothing else; any other
// call is a test bug.
type failingGet struct {
	driveclient.Client
	err error
}

func (f *failingGet) Get(context.Context, string) (driveclient.File, error) {
	return driveclient.File{}, f.err
}
