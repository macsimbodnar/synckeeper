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

// R4 (spec §7 overwrite guard): a local edit landing between the scan and
// the download's atomic rename must win the cycle — the rename is refused
// when the target no longer matches the stat the plan carried.
func TestR4DownloadRefusedWhenTargetDriftsMidCycle(t *testing.T) {
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
	remote, err := fake.Upload(ctx, folder.ID, "f.txt", strings.NewReader("remote-v2"), 9)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(syncDir, "f.txt")
	if err := os.WriteFile(target, []byte("local-v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanned, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}

	// The "user" edits the file after the scan, while the download is
	// already in flight (temp written, rename not yet performed).
	FaultHook = func(cp string) error {
		if cp == CPDownloadTempWritten {
			return os.WriteFile(target, []byte("mid-cycle edit"), 0o644)
		}
		return nil
	}
	defer func() { FaultHook = nil }()

	x := &Executor{DB: db, Client: fake, SyncDir: syncDir,
		QuarantineDir: filepath.Join(base, "quarantine"), RootID: folder.ID}
	sum, err := x.Apply(ctx, []reconcile.Action{
		{Type: reconcile.Download, RelPath: "f.txt", FileID: remote.ID,
			MD5: remote.MD5, Size: remote.Size,
			LocalExists: true, LocalSize: scanned.Size(), LocalMtimeNS: scanned.ModTime().UnixNano()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Failed != 1 || sum.Executed != 0 {
		t.Fatalf("executed/failed = %d/%d, want 0/1 (download refused): %v", sum.Executed, sum.Failed, sum.Errors)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "mid-cycle edit" {
		t.Fatalf("target = %q, want the mid-cycle edit preserved", got)
	}
}

// R4, appearance direction: a download whose target the plan expected to be
// absent (new remote file, conflict-vacated path) must be refused if a file
// appeared there mid-cycle.
func TestR4DownloadRefusedWhenTargetAppearsMidCycle(t *testing.T) {
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
	remote, err := fake.Upload(ctx, folder.ID, "new.txt", strings.NewReader("remote"), 6)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(syncDir, "new.txt")
	FaultHook = func(cp string) error {
		if cp == CPDownloadTempWritten {
			return os.WriteFile(target, []byte("appeared mid-cycle"), 0o644)
		}
		return nil
	}
	defer func() { FaultHook = nil }()

	x := &Executor{DB: db, Client: fake, SyncDir: syncDir,
		QuarantineDir: filepath.Join(base, "quarantine"), RootID: folder.ID}
	sum, err := x.Apply(ctx, []reconcile.Action{
		{Type: reconcile.Download, RelPath: "new.txt", FileID: remote.ID,
			MD5: remote.MD5, Size: remote.Size}, // LocalExists=false: plan saw nothing here
	})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Failed != 1 {
		t.Fatalf("failed = %d, want 1 (download refused): %v", sum.Failed, sum.Errors)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "appeared mid-cycle" {
		t.Fatalf("target = %q, want the appeared file preserved", got)
	}
}

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
