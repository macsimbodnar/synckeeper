package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// Invariant 7 (spec §4.5): an action whose protecting conflict backup failed
// must not execute. The backup here fails naturally (its destination
// directory does not exist); the protected download must then be refused,
// leaving the local content — the only copy of a local edit — untouched.
func TestProtectedDownloadRefusedWhenBackupFails(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	syncDir := filepath.Join(base, "sync")
	if err := os.MkdirAll(syncDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := statedb.Open(filepath.Join(base, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	fake := driveclient.NewFake()
	folder, err := fake.Mkdir(ctx, driveclient.FakeRootID, "Synckeeper")
	if err != nil {
		t.Fatal(err)
	}
	remote, err := fake.Upload(ctx, folder.ID, "f.txt", strings.NewReader("remote"), 6)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(syncDir, "f.txt"), []byte("precious local edit"), 0o644); err != nil {
		t.Fatal(err)
	}

	x := &Executor{DB: db, Client: fake, SyncDir: syncDir,
		QuarantineDir: filepath.Join(base, "quarantine"), RootID: folder.ID}
	plan := []reconcile.Action{
		{Type: reconcile.ConflictBackup, RelPath: "f.txt", NewRelPath: "missing-dir/f (conflict).txt"},
		{Type: reconcile.Download, RelPath: "f.txt", FileID: remote.ID,
			MD5: remote.MD5, Size: remote.Size, ProtectedBy: "f.txt"},
	}
	sum, err := x.Apply(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Failed != 2 || sum.Executed != 0 {
		t.Fatalf("executed/failed = %d/%d, want 0/2 (backup fails, download refused): %v",
			sum.Executed, sum.Failed, sum.Errors)
	}
	got, err := os.ReadFile(filepath.Join(syncDir, "f.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "precious local edit" {
		t.Fatalf("local content = %q, want it untouched", got)
	}
}
