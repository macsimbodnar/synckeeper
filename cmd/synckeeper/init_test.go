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

	if _, err := initialize(ctx, fake, db, cfg); err != nil {
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
	if _, err := initialize(ctx, fake, db, config.Default()); err != nil {
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
	if _, err := initialize(ctx, driveclient.NewFake(), db, config.Default()); err != nil {
		t.Fatal(err)
	}
	first, _ := db.GetMeta(statedb.MetaMachineID)
	if _, err := initialize(ctx, driveclient.NewFake(), db, config.Default()); err != nil {
		t.Fatal(err)
	}
	second, _ := db.GetMeta(statedb.MetaMachineID)
	if first == "" || first != second {
		t.Errorf("machine_id changed across re-init: %q -> %q", first, second)
	}
}

// W18-B: `init` no longer refuses a non-empty Drive folder — joining one is a
// union merge that cannot delete (spec §11), so making the user assert the
// intention with --adopt only ever taught them to pass a flag unread. The
// replacement property is stronger and is what a user would notice: joining
// succeeds, resolves the EXISTING folder rather than minting one, and is
// idempotent across repeated runs.
func TestInitializeJoinsNonEmptyFolderIdempotently(t *testing.T) {
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
	got, err := initialize(ctx, fake, db, config.Default())
	if err != nil {
		t.Fatalf("init must join a non-empty folder, not refuse: %v", err)
	}
	if got != folder.ID {
		t.Errorf("resolved %s, want the existing folder %s", got, folder.ID)
	}

	// Re-running changes nothing: same folder, no second folder on Drive.
	again, err := initialize(ctx, fake, db, config.Default())
	if err != nil {
		t.Fatalf("re-running init must be a no-op, not an error: %v", err)
	}
	if again != folder.ID {
		t.Errorf("second run resolved %s, want %s", again, folder.ID)
	}
	children, err := fake.List(ctx, driveclient.FakeRootID)
	if err != nil {
		t.Fatal(err)
	}
	folders := 0
	for _, c := range children {
		if c.IsDir() && c.Name == "Synckeeper" {
			folders++
		}
	}
	if folders != 1 {
		t.Errorf("%d folders named Synckeeper on Drive, want 1 — re-init must not mint another", folders)
	}
}

// R3 regression (2026-07-17, testing.md): a re-run of `init` must rebuild the
// remote mirror. Resetting only the page token silently skipped every remote
// change made since the last consumed batch: the stale mirror said "remote
// unchanged", the newer revision was never downloaded, and a later local
// edit would have uploaded over it — invisible divergence.
func TestR3ForceReinitSeesPriorRemoteChanges(t *testing.T) {
	ctx := context.Background()
	fake := driveclient.NewFake()
	db := openTestDB(t)
	cfg := config.Default()

	rootID, err := initialize(ctx, fake, db, cfg)
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
	// …then the user re-inits and syncs. Since W18-B that needs no flags:
	// `init` is idempotent and always merges.
	if _, err := initialize(ctx, fake, db, cfg); err != nil {
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
		t.Errorf("f.txt = %q after re-init; the pre-reinit remote edit was silently dropped (want %q)", got, "v2-remote")
	}
}

func TestInitializeSurfacesClientErrors(t *testing.T) {
	// A fake with no root folder makes List fail; initialize must propagate.
	db := openTestDB(t)
	_, err := initialize(context.Background(), brokenClient{}, db, config.Default())
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
