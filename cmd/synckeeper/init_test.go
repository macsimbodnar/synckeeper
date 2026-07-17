package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/config"
	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/engine"
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

// R3 regression (2026-07-17, testing.md): `init --force` must rebuild the
// remote mirror. Resetting only the page token silently skipped every remote
// change made since the last consumed batch: the stale mirror said "remote
// unchanged", the newer revision was never downloaded, and a later local
// edit would have uploaded over it — invisible divergence.
func TestR3ForceReinitSeesPriorRemoteChanges(t *testing.T) {
	ctx := context.Background()
	fake := driveclient.NewFake()
	db := openTestDB(t)
	cfg := config.Default()

	rootID, err := initialize(ctx, fake, db, cfg, false)
	if err != nil {
		t.Fatal(err)
	}

	// A remote file appears and one sync consumes it.
	remote, err := fake.Upload(ctx, rootID, "f.txt", strings.NewReader("v1"), 2)
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	syncDir := filepath.Join(base, "sync")
	if err := os.MkdirAll(syncDir, 0o755); err != nil {
		t.Fatal(err)
	}
	eng := &engine.Engine{DB: db, Client: fake, Cfg: cfg, SyncDir: syncDir,
		QuarantineDir: filepath.Join(base, "quarantine"), RootID: rootID}
	if res, err := eng.Sync(ctx, engine.Options{}); err != nil || res.Failed > 0 {
		t.Fatalf("first sync: err=%v result=%+v", err, res)
	}

	// The file changes remotely while this machine is not syncing…
	if _, err := fake.Update(ctx, remote.ID, strings.NewReader("v2-remote"), 9); err != nil {
		t.Fatal(err)
	}
	// …then the user re-inits (`init --force --adopt`: force passes the
	// reinit gate, adopt passes the non-empty-folder gate) and syncs.
	if _, err := initialize(ctx, fake, db, cfg, true); err != nil {
		t.Fatal(err)
	}
	if res, err := eng.Sync(ctx, engine.Options{}); err != nil || res.Failed > 0 {
		t.Fatalf("post-reinit sync: err=%v result=%+v", err, res)
	}

	got, err := os.ReadFile(filepath.Join(syncDir, "f.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v2-remote" {
		t.Errorf("f.txt = %q after force re-init; the pre-reinit remote edit was silently dropped (want %q)", got, "v2-remote")
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
