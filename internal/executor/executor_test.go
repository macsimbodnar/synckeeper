package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// R7 (spec §7 overwrite guard, moves): a MoveLocal must never clobber a file
// the plan did not account for. When the destination is occupied and the
// action did not expect an occupant, the move is refused and the occupant
// preserved.
func TestR7MoveLocalRefusesUnexpectedOccupant(t *testing.T) {
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

	// The file being moved, and an unrelated precious file already at the dest.
	if err := os.WriteFile(filepath.Join(syncDir, "from.txt"), []byte("moving"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(syncDir, "to.txt"), []byte("PRECIOUS"), 0o644); err != nil {
		t.Fatal(err)
	}

	x := &Executor{DB: db, Client: driveclient.NewFake(), SyncDir: syncDir,
		QuarantineDir: filepath.Join(base, "quarantine"), RootID: "root"}
	// LocalExists is false: the plan believed the destination was empty.
	sum, err := x.Apply(ctx, []reconcile.Action{
		{Type: reconcile.MoveLocal, RelPath: "from.txt", NewRelPath: "to.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Failed != 1 || sum.Executed != 0 {
		t.Fatalf("executed/failed = %d/%d, want 0/1 (move refused): %v", sum.Executed, sum.Failed, sum.Errors)
	}
	if got, _ := os.ReadFile(filepath.Join(syncDir, "to.txt")); string(got) != "PRECIOUS" {
		t.Fatalf("to.txt = %q, want the precious occupant untouched", got)
	}
	if got, _ := os.ReadFile(filepath.Join(syncDir, "from.txt")); string(got) != "moving" {
		t.Fatalf("from.txt = %q, want the source left in place for the next cycle", got)
	}
}

// R8 (Finding 2): a ConflictBackup must never overwrite an existing file at its
// destination (a crash-leftover copy with a colliding timestamped name).
func TestR8ConflictBackupRefusesExistingDestination(t *testing.T) {
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

	if err := os.WriteFile(filepath.Join(syncDir, "f.txt"), []byte("current"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(syncDir, "f (conflict).txt"), []byte("earlier-copy"), 0o644); err != nil {
		t.Fatal(err)
	}

	x := &Executor{DB: db, Client: driveclient.NewFake(), SyncDir: syncDir,
		QuarantineDir: filepath.Join(base, "quarantine"), RootID: "root"}
	sum, err := x.Apply(ctx, []reconcile.Action{
		{Type: reconcile.ConflictBackup, RelPath: "f.txt", NewRelPath: "f (conflict).txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Failed != 1 {
		t.Fatalf("failed = %d, want 1 (backup refused): %v", sum.Failed, sum.Errors)
	}
	if got, _ := os.ReadFile(filepath.Join(syncDir, "f (conflict).txt")); string(got) != "earlier-copy" {
		t.Fatalf("existing conflict copy = %q, want it untouched", got)
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

// R13 (spec §7, the local-write gate's first customer): a local edit landing
// between the scan and the quarantine must win the cycle — "edit beats
// delete, always" (§4.2). The quarantine is refused when the source no
// longer matches the stat the plan carried.
func TestR13QuarantineLocalRefusesDriftedSource(t *testing.T) {
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

	target := filepath.Join(syncDir, "f.txt")
	if err := os.WriteFile(target, []byte("scanned content"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanned, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	// The mid-cycle edit: lands after the scan, before the executor runs.
	if err := os.WriteFile(target, []byte("edited after the scan"), 0o644); err != nil {
		t.Fatal(err)
	}

	x := &Executor{DB: db, Client: fake, SyncDir: syncDir,
		QuarantineDir: filepath.Join(base, "quarantine"), RootID: folder.ID}
	sum, err := x.Apply(ctx, []reconcile.Action{{
		Type: reconcile.QuarantineLocal, RelPath: "f.txt", FileID: "some-id",
		LocalExists: true, LocalSize: scanned.Size(), LocalMtimeNS: scanned.ModTime().UnixNano(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Failed != 1 {
		t.Fatalf("quarantine of a drifted source must be refused; got executed=%d failed=%d", sum.Executed, sum.Failed)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("the edited file must survive in place: %v", err)
	}
	if string(got) != "edited after the scan" {
		t.Fatalf("local content = %q, want the mid-cycle edit intact", got)
	}
}

// R13 companion: a quarantine whose pinned stat still matches proceeds — the
// guard must not over-refuse the normal delete path.
func TestR13QuarantineLocalProceedsWhenPinMatches(t *testing.T) {
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

	target := filepath.Join(syncDir, "f.txt")
	if err := os.WriteFile(target, []byte("stable content"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanned, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}

	x := &Executor{DB: db, Client: fake, SyncDir: syncDir,
		QuarantineDir: filepath.Join(base, "quarantine"), RootID: folder.ID}
	sum, err := x.Apply(ctx, []reconcile.Action{{
		Type: reconcile.QuarantineLocal, RelPath: "f.txt", FileID: "some-id",
		LocalExists: true, LocalSize: scanned.Size(), LocalMtimeNS: scanned.ModTime().UnixNano(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Failed != 0 {
		t.Fatalf("matching pin must quarantine cleanly; failed=%d errors=%v", sum.Failed, sum.Errors)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatal("file should have left the sync dir")
	}
	quarantined := filepath.Join(base, "quarantine", time.Now().Format("2006-01-02"), "f.txt")
	if got, err := os.ReadFile(quarantined); err != nil || string(got) != "stable content" {
		t.Fatalf("rescue copy missing or wrong: %q, %v", got, err)
	}
}
