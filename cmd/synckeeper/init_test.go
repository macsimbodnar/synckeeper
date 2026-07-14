package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/config"
	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

func openTestDB(t *testing.T) *statedb.DB {
	t.Helper()
	db, err := statedb.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestInitializeCreatesFolderAndMeta(t *testing.T) {
	ctx := context.Background()
	fake := driveclient.NewFake()
	db := openTestDB(t)
	cfg := config.Default()

	if _, err := initialize(ctx, fake, db, cfg, false); err != nil {
		t.Fatal(err)
	}

	rootID, err := db.GetMeta(statedb.MetaRootFolderID)
	if err != nil {
		t.Fatal(err)
	}
	folder, err := fake.Get(ctx, rootID)
	if err != nil {
		t.Fatal(err)
	}
	if !folder.IsDir() || folder.Name != cfg.Drive.FolderName {
		t.Errorf("root folder = %+v, want folder named %q", folder, cfg.Drive.FolderName)
	}
	if tok, err := db.GetMeta(statedb.MetaPageToken); err != nil || tok == "" {
		t.Errorf("page_token = %q, %v; want non-empty", tok, err)
	}
	if id, err := db.GetMeta(statedb.MetaMachineID); err != nil || id == "" {
		t.Errorf("machine_id = %q, %v; want non-empty", id, err)
	}
}

func TestInitializeAdoptsExistingFolder(t *testing.T) {
	ctx := context.Background()
	fake := driveclient.NewFake()
	existing, err := fake.Mkdir(ctx, driveclient.FakeRootID, "Synckeeper")
	if err != nil {
		t.Fatal(err)
	}
	db := openTestDB(t)

	// An existing but EMPTY folder is reused without --adopt (no content to
	// merge, so no risk of joining someone else's data unknowingly).
	if _, err := initialize(ctx, fake, db, config.Default(), false); err != nil {
		t.Fatal(err)
	}
	rootID, _ := db.GetMeta(statedb.MetaRootFolderID)
	if rootID != existing.ID {
		t.Errorf("root folder id = %s, want existing %s (must not create a duplicate)", rootID, existing.ID)
	}
	children, _ := fake.List(ctx, driveclient.FakeRootID)
	folders := 0
	for _, c := range children {
		if c.IsDir() && c.Name == "Synckeeper" {
			folders++
		}
	}
	if folders != 1 {
		t.Errorf("found %d Synckeeper folders at root, want 1", folders)
	}
}

func TestInitializeKeepsMachineID(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if _, err := initialize(ctx, driveclient.NewFake(), db, config.Default(), false); err != nil {
		t.Fatal(err)
	}
	first, _ := db.GetMeta(statedb.MetaMachineID)
	if _, err := initialize(ctx, driveclient.NewFake(), db, config.Default(), false); err != nil {
		t.Fatal(err)
	}
	second, _ := db.GetMeta(statedb.MetaMachineID)
	if first == "" || first != second {
		t.Errorf("machine_id changed across re-init: %q -> %q", first, second)
	}
}

// A Drive folder that already holds files is refused without --adopt, and
// nothing is persisted so an --adopt retry works. With --adopt it proceeds.
func TestInitializeRefusesNonEmptyWithoutAdopt(t *testing.T) {
	ctx := context.Background()
	fake := driveclient.NewFake()
	folder, err := fake.Mkdir(ctx, driveclient.FakeRootID, "Synckeeper")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fake.Upload(ctx, folder.ID, "existing.txt", strings.NewReader("hi"), 2); err != nil {
		t.Fatal(err)
	}

	db := openTestDB(t)
	if _, err := initialize(ctx, fake, db, config.Default(), false); err == nil {
		t.Fatal("want error joining a non-empty folder without --adopt")
	}
	if _, err := db.GetMeta(statedb.MetaRootFolderID); !errors.Is(err, statedb.ErrNotFound) {
		t.Error("root_folder_id was persisted despite the refusal")
	}

	// --adopt proceeds.
	if _, err := initialize(ctx, fake, db, config.Default(), true); err != nil {
		t.Fatalf("--adopt should join a non-empty folder: %v", err)
	}
	if id, _ := db.GetMeta(statedb.MetaRootFolderID); id != folder.ID {
		t.Errorf("root folder id = %s, want %s", id, folder.ID)
	}
}

func TestRefuseReinit(t *testing.T) {
	db := openTestDB(t)
	if err := refuseReinit(db, false); err != nil {
		t.Fatalf("fresh db: %v", err)
	}
	if err := db.SetMeta(statedb.MetaRootFolderID, "x"); err != nil {
		t.Fatal(err)
	}
	if err := refuseReinit(db, false); err == nil {
		t.Fatal("initialized db without --force: want error")
	}
	if err := refuseReinit(db, true); err != nil {
		t.Fatalf("initialized db with --force: %v", err)
	}
}

func TestInitializeSurfacesClientErrors(t *testing.T) {
	// A fake with no root folder makes List fail; initialize must propagate.
	db := openTestDB(t)
	_, err := initialize(context.Background(), brokenClient{}, db, config.Default(), false)
	if err == nil {
		t.Fatal("want error from broken client")
	}
	if _, metaErr := db.GetMeta(statedb.MetaRootFolderID); !errors.Is(metaErr, statedb.ErrNotFound) {
		t.Error("root_folder_id was written despite failure")
	}
}

type brokenClient struct{ driveclient.Client }

func (brokenClient) List(context.Context, string) ([]driveclient.File, error) {
	return nil, errors.New("boom")
}
