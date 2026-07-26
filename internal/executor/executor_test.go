package executor

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/names"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
	"github.com/macsimbodnar/synckeeper/internal/trash"
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

// R13 companion: a remote-deletion removal whose pinned stat still matches
// proceeds — the guard must not over-refuse the normal delete path. Since
// W13 the destination is the system bin.
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

	bin := trash.NewFake(filepath.Join(base, "bin"))
	x := &Executor{DB: db, Client: fake, SyncDir: syncDir,
		QuarantineDir: filepath.Join(base, "quarantine"), RootID: folder.ID, Trash: bin}
	sum, err := x.Apply(ctx, []reconcile.Action{{
		Type: reconcile.QuarantineLocal, RelPath: "f.txt", FileID: "some-id",
		LocalExists: true, LocalSize: scanned.Size(), LocalMtimeNS: scanned.ModTime().UnixNano(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Failed != 0 {
		t.Fatalf("matching pin must remove cleanly; failed=%d errors=%v", sum.Failed, sum.Errors)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatal("file should have left the sync dir")
	}
	if got, err := os.ReadFile(filepath.Join(bin.Dir, "f.txt")); err != nil || string(got) != "stable content" {
		t.Fatalf("rescued copy missing or wrong in the bin: %q, %v", got, err)
	}
}

// R12: a plan with two transfer-stage actions on one rel_path is refused
// before anything executes — a planner mistake surfaces as a refused plan,
// not a race (spec §4.5).
func TestR12ApplyRefusesOverlappingTransferStage(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(syncDir, "x"), []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}

	x := &Executor{DB: db, Client: fake, SyncDir: syncDir,
		QuarantineDir: filepath.Join(base, "quarantine"), RootID: folder.ID}
	_, err = x.Apply(ctx, []reconcile.Action{
		{Type: reconcile.Upload, RelPath: "x"},
		{Type: reconcile.Download, RelPath: "x", FileID: "someid"},
	})
	if err == nil {
		t.Fatal("overlapping transfer stage must refuse the whole plan")
	}
	n, err := db.PendingOpCount()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("refused plan left %d journaled ops; nothing may execute", n)
	}
}

// R20 (C3, spec §3 invariant 3): a removed directory carries its invisible
// leftovers — ignored and temp files, the only content the plan cannot see
// by design — with it instead of wedging on "directory not empty" forever.
// Since W13 they ride along inside the folder as it moves to the bin.
func TestR20QuarantinedDirSweepsIgnoredLeftovers(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	syncDir := filepath.Join(base, "sync")
	if err := os.MkdirAll(filepath.Join(syncDir, "docs"), 0o755); err != nil {
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
	// The two invisible leftovers: an ignored file and one of our temps.
	if err := os.WriteFile(filepath.Join(syncDir, "docs", ".DS_Store"), []byte("finder"), 0o644); err != nil {
		t.Fatal(err)
	}
	tmpName := names.TempPrefix + "orphan"
	if err := os.WriteFile(filepath.Join(syncDir, "docs", tmpName), []byte("temp"), 0o644); err != nil {
		t.Fatal(err)
	}

	quarantine := filepath.Join(base, "quarantine")
	bin := trash.NewFake(filepath.Join(base, "bin"))
	x := &Executor{DB: db, Client: fake, SyncDir: syncDir,
		QuarantineDir: quarantine, RootID: folder.ID,
		Ignore: []string{".DS_Store"}, Trash: bin}
	sum, err := x.Apply(ctx, []reconcile.Action{{
		Type: reconcile.QuarantineLocal, RelPath: "docs", FileID: "dir-id", IsDir: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Failed != 0 {
		t.Fatalf("removal of a dir with only invisible leftovers failed: %v", sum.Errors)
	}
	if _, err := os.Stat(filepath.Join(syncDir, "docs")); !os.IsNotExist(err) {
		t.Error("docs still present after removal")
	}
	if _, err := os.Stat(filepath.Join(bin.Dir, "docs", ".DS_Store")); err != nil {
		t.Errorf("ignored leftover not carried into the bin with the dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bin.Dir, "docs", tmpName)); err != nil {
		t.Errorf("temp leftover not carried into the bin with the dir: %v", err)
	}
}

// R20, fallback road: with no usable bin the directory still cannot wedge —
// the invisible leftovers are swept into the quarantine and the empty dir is
// removed, exactly as before W13.
func TestR20DirWithoutTrashFallsBackToQuarantineSweep(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	syncDir := filepath.Join(base, "sync")
	if err := os.MkdirAll(filepath.Join(syncDir, "docs"), 0o755); err != nil {
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
	if err := os.WriteFile(filepath.Join(syncDir, "docs", ".DS_Store"), []byte("finder"), 0o644); err != nil {
		t.Fatal(err)
	}

	quarantine := filepath.Join(base, "quarantine")
	x := &Executor{DB: db, Client: fake, SyncDir: syncDir,
		QuarantineDir: quarantine, RootID: folder.ID,
		Ignore: []string{".DS_Store"}, Trash: &trash.Fake{Unavailable: true}}
	sum, err := x.Apply(ctx, []reconcile.Action{{
		Type: reconcile.QuarantineLocal, RelPath: "docs", FileID: "dir-id", IsDir: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Failed != 0 {
		t.Fatalf("quarantine fallback for a dir failed: %v", sum.Errors)
	}
	if _, err := os.Stat(filepath.Join(syncDir, "docs")); !os.IsNotExist(err) {
		t.Error("docs still present after the fallback removal")
	}
	day := time.Now().Format("2006-01-02")
	if _, err := os.Stat(filepath.Join(quarantine, day, "docs", ".DS_Store")); err != nil {
		t.Errorf("ignored leftover not carried into quarantine: %v", err)
	}
}

// R20 companion: anything the sweep does not recognize still refuses —
// an unexpected survivor means the plan is wrong, and data stays put.
func TestR20QuarantinedDirStillRefusesUnexpectedSurvivor(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	syncDir := filepath.Join(base, "sync")
	if err := os.MkdirAll(filepath.Join(syncDir, "docs"), 0o755); err != nil {
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
	if err := os.WriteFile(filepath.Join(syncDir, "docs", "real.txt"), []byte("user data"), 0o644); err != nil {
		t.Fatal(err)
	}

	x := &Executor{DB: db, Client: fake, SyncDir: syncDir,
		QuarantineDir: filepath.Join(base, "quarantine"), RootID: folder.ID,
		Ignore: []string{".DS_Store"}}
	sum, err := x.Apply(ctx, []reconcile.Action{{
		Type: reconcile.QuarantineLocal, RelPath: "docs", FileID: "dir-id", IsDir: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Failed != 1 {
		t.Fatalf("unexpected survivor must refuse the dir removal, got %+v", sum)
	}
	if raw, err := os.ReadFile(filepath.Join(syncDir, "docs", "real.txt")); err != nil || string(raw) != "user data" {
		t.Errorf("survivor was touched: %q, %v", raw, err)
	}
}

// R21 (C4, spec §3 invariant 3): two same-day quarantines of one rel_path
// keep BOTH rescue copies — the destination uniquifies with a numbered
// suffix (was: the second os.Rename silently destroyed the first copy).
func TestR21QuarantineNeverOverwritesRescueCopy(t *testing.T) {
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
	quarantine := filepath.Join(base, "quarantine")
	// The quarantine is the fallback road since W13 (T13.4); its rescue
	// copies must still never overwrite each other.
	x := &Executor{DB: db, Client: fake, SyncDir: syncDir,
		QuarantineDir: quarantine, RootID: folder.ID, Trash: &trash.Fake{Unavailable: true}}

	target := filepath.Join(syncDir, "f.txt")
	quarantineOnce := func(content string) {
		t.Helper()
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		scanned, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		sum, err := x.Apply(ctx, []reconcile.Action{{
			Type: reconcile.QuarantineLocal, RelPath: "f.txt", FileID: "id-" + content,
			LocalExists: true, LocalSize: scanned.Size(), LocalMtimeNS: scanned.ModTime().UnixNano(),
		}})
		if err != nil {
			t.Fatal(err)
		}
		if sum.Failed != 0 {
			t.Fatalf("quarantine of %q failed: %v", content, sum.Errors)
		}
	}
	quarantineOnce("v1")
	quarantineOnce("v2")

	got := map[string]bool{}
	filepath.WalkDir(quarantine, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			raw, _ := os.ReadFile(p)
			got[string(raw)] = true
		}
		return nil
	})
	if !got["v1"] || !got["v2"] {
		t.Errorf("rescue copies lost: quarantine holds contents %v, want v1 and v2", got)
	}
}

// R21 companion: guardedMoveFile itself refuses an occupied destination —
// belt and braces beneath the uniquifier, so a race between the name pick
// and the rename can never clobber a rescue copy.
func TestR21GuardedMoveFileRefusesOccupiedDestination(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src.txt")
	dst := filepath.Join(base, "dst.txt")
	if err := os.WriteFile(src, []byte("moving"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("earlier rescue copy"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanned, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	exp := expectation{exists: true, size: scanned.Size(), mtimeNS: scanned.ModTime().UnixNano()}
	if err := guardedMoveFile(src, dst, "test move", exp); err == nil {
		t.Fatal("occupied destination must refuse")
	}
	if raw, _ := os.ReadFile(dst); string(raw) != "earlier rescue copy" {
		t.Errorf("destination clobbered: %q", raw)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("source vanished on a refused move: %v", err)
	}
}
