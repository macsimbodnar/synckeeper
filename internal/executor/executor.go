// Package executor applies a reconcile plan: journals every action to
// pending_ops, performs the I/O with the atomic write protocol, and commits
// each baseline row in the same transaction that marks its op done.
package executor

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/names"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

const transferWorkers = 4

// Fault-injection checkpoints (fault tests F1–F3). FaultHook, when set by a
// test, runs at each named checkpoint; returning an error aborts the op
// right there, leaving exactly the state a crash at that point would leave
// (minus in-process cleanup that a real crash would skip — the planted-state
// tests cover those paths). Always nil in production.
var FaultHook func(checkpoint string) error

const (
	CPUploadBeforeCommit   = "upload_before_commit"   // remote has the file, DB does not
	CPDownloadTempWritten  = "download_temp_written"  // temp complete, target untouched
	CPDownloadBeforeCommit = "download_before_commit" // target replaced, DB row still old
)

func checkpoint(name string) error {
	if FaultHook != nil {
		return FaultHook(name)
	}
	return nil
}

// Executor applies plans for one sync run.
type Executor struct {
	DB            *statedb.DB
	Client        driveclient.Client
	SyncDir       string
	QuarantineDir string
	RootID        string

	mu            sync.Mutex
	pathIDs       map[string]string // rel_path -> drive file id, for parent lookup
	failedBackups map[string]bool   // ConflictBackup sources that failed this run (invariant 7)
}

// Summary reports what happened.
type Summary struct {
	Executed int
	Failed   int
	Errors   []string
}

// Apply journals and executes the plan. Individual action failures are
// collected, not fatal: the rest of the plan proceeds where safe and the
// next run replans.
func (x *Executor) Apply(ctx context.Context, plan []reconcile.Action) (Summary, error) {
	items, err := x.DB.AllItems()
	if err != nil {
		return Summary{}, err
	}
	x.pathIDs = map[string]string{"": x.RootID}
	for _, it := range items {
		x.pathIDs[it.RelPath] = it.DriveFileID
	}
	x.failedBackups = map[string]bool{}

	ops := make([]statedb.PendingOp, len(plan))
	for i, a := range plan {
		payload, err := json.Marshal(a)
		if err != nil {
			return Summary{}, err
		}
		ops[i] = statedb.PendingOp{Type: string(a.Type), RelPath: a.RelPath, FileID: a.FileID, Payload: string(payload)}
	}
	ids, err := x.DB.AddOps(ops)
	if err != nil {
		return Summary{}, err
	}

	var sum Summary
	var sumMu sync.Mutex
	run := func(i int) {
		a := plan[i]
		if err := x.DB.SetOpState(ids[i], statedb.OpInProgress); err != nil {
			x.noteFailure(a)
			sumMu.Lock()
			sum.Failed++
			sum.Errors = append(sum.Errors, fmt.Sprintf("%s %s: journal: %v", a.Type, a.RelPath, err))
			sumMu.Unlock()
			return
		}
		err := x.execute(ctx, ids[i], a)
		sumMu.Lock()
		defer sumMu.Unlock()
		if err != nil {
			x.noteFailure(a)
			sum.Failed++
			sum.Errors = append(sum.Errors, fmt.Sprintf("%s %s: %v", a.Type, a.RelPath, err))
			slog.Error("action failed", "type", a.Type, "rel_path", a.RelPath, "err", err)
		} else {
			sum.Executed++
			slog.Debug("action done", "type", a.Type, "rel_path", a.RelPath)
		}
	}

	// Serial stages before/after a parallel transfer stage. Stage
	// boundaries follow the plan's ordering contract.
	isTransfer := func(t reconcile.Type) bool {
		switch t {
		case reconcile.Upload, reconcile.UpdateRemote, reconcile.Download, reconcile.Record:
			return true
		}
		return false
	}
	i := 0
	for i < len(plan) && !isTransfer(plan[i].Type) {
		run(i)
		i++
	}
	firstTransfer := i
	for i < len(plan) && isTransfer(plan[i].Type) {
		i++
	}
	if i > firstTransfer {
		var wg sync.WaitGroup
		work := make(chan int)
		for w := 0; w < transferWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for idx := range work {
					run(idx)
				}
			}()
		}
		for idx := firstTransfer; idx < i; idx++ {
			work <- idx
		}
		close(work)
		wg.Wait()
	}
	for ; i < len(plan); i++ {
		run(i)
	}

	if sum.Failed == 0 {
		if err := x.DB.ClearOps(); err != nil {
			return sum, err
		}
	}
	return sum, nil
}

// CleanStaleState removes leftover temp files and journal rows from a
// previous crashed run. The regenerated plan supersedes stale ops; partial
// effects are self-healing through the reconcile decision table.
func CleanStaleState(db *statedb.DB, syncDir string) error {
	stale, err := db.StaleOps()
	if err != nil {
		return err
	}
	if len(stale) > 0 {
		slog.Info("discarding stale pending ops from a previous run; replanning", "count", len(stale))
	}
	if err := db.ClearOps(); err != nil {
		return err
	}
	return filepath.WalkDir(syncDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: the scan proper will complain
		}
		if !d.IsDir() && strings.HasPrefix(d.Name(), names.TempPrefix) {
			slog.Info("removing orphan temp file", "path", p)
			os.Remove(p)
		}
		return nil
	})
}

func (x *Executor) abs(rel string) string {
	return filepath.Join(x.SyncDir, filepath.FromSlash(rel))
}

func (x *Executor) parentID(rel string) (string, error) {
	dir := path.Dir(rel)
	if dir == "." {
		dir = ""
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	id, ok := x.pathIDs[dir]
	if !ok {
		return "", fmt.Errorf("no drive id known for parent folder %q", dir)
	}
	return id, nil
}

func (x *Executor) setPathID(rel, id string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.pathIDs[rel] = id
}

// noteFailure records a failed conflict backup so actions it protects are
// refused for the rest of the run (invariant 7).
func (x *Executor) noteFailure(a reconcile.Action) {
	if a.Type != reconcile.ConflictBackup {
		return
	}
	x.mu.Lock()
	x.failedBackups[a.RelPath] = true
	x.mu.Unlock()
}

func (x *Executor) execute(ctx context.Context, opID int64, a reconcile.Action) error {
	// Invariant 7: destruction never outruns protection. Backups run in the
	// serial moves stage, so their outcomes are known before any protected
	// transfer starts.
	if a.ProtectedBy != "" {
		x.mu.Lock()
		failed := x.failedBackups[a.ProtectedBy]
		x.mu.Unlock()
		if failed {
			return fmt.Errorf("not executed: the conflict backup of %s failed and this action depends on it; local content preserved, replanning next cycle", a.ProtectedBy)
		}
	}
	switch a.Type {
	case reconcile.MkdirLocal:
		return x.mkdirLocal(opID, a)
	case reconcile.MkdirRemote:
		return x.mkdirRemote(ctx, opID, a)
	case reconcile.MoveLocal:
		return x.moveLocal(opID, a)
	case reconcile.MoveRemote:
		return x.moveRemote(ctx, opID, a)
	case reconcile.ConflictBackup:
		return x.conflictBackup(opID, a)
	case reconcile.Upload:
		return x.upload(ctx, opID, a)
	case reconcile.UpdateRemote:
		return x.updateRemote(ctx, opID, a)
	case reconcile.Download:
		return x.download(ctx, opID, a)
	case reconcile.Record:
		return x.record(opID, a)
	case reconcile.TrashRemote:
		return x.trashRemote(ctx, opID, a)
	case reconcile.QuarantineLocal:
		return x.quarantineLocal(opID, a)
	case reconcile.Forget:
		return x.DB.CompleteOp(opID, func(tx *sql.Tx) error {
			return statedb.DeleteItemByID(tx, a.FileID)
		})
	default:
		return fmt.Errorf("unknown action type %q", a.Type)
	}
}

func (x *Executor) mkdirLocal(opID int64, a reconcile.Action) error {
	if err := os.MkdirAll(x.abs(a.RelPath), 0o755); err != nil {
		return err
	}
	x.setPathID(a.RelPath, a.FileID)
	return x.DB.CompleteOp(opID, func(tx *sql.Tx) error {
		return statedb.UpsertItem(tx, statedb.Item{
			DriveFileID: a.FileID, RelPath: a.RelPath, IsDir: true, DriveVersion: a.Version,
		})
	})
}

func (x *Executor) mkdirRemote(ctx context.Context, opID int64, a reconcile.Action) error {
	parent, err := x.parentID(a.RelPath)
	if err != nil {
		return err
	}
	f, err := x.Client.Mkdir(ctx, parent, path.Base(a.RelPath))
	if err != nil {
		return err
	}
	// Ensure the local dir exists too (resurrect case already has it).
	if err := os.MkdirAll(x.abs(a.RelPath), 0o755); err != nil {
		return err
	}
	x.setPathID(a.RelPath, f.ID)
	return x.DB.CompleteOp(opID, func(tx *sql.Tx) error {
		return statedb.UpsertItem(tx, statedb.Item{
			DriveFileID: f.ID, RelPath: a.RelPath, IsDir: true, DriveVersion: f.Version,
		})
	})
}

func (x *Executor) moveLocal(opID int64, a reconcile.Action) error {
	from, to := x.abs(a.RelPath), x.abs(a.NewRelPath)
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	if err := os.Rename(from, to); err != nil {
		return err
	}
	fsyncDir(filepath.Dir(to))
	x.renamePathIDs(a.RelPath, a.NewRelPath)
	return x.DB.CompleteOp(opID, func(tx *sql.Tx) error {
		return statedb.RenameItemPath(tx, a.RelPath, a.NewRelPath)
	})
}

func (x *Executor) moveRemote(ctx context.Context, opID int64, a reconcile.Action) error {
	parent, err := x.parentID(a.NewRelPath)
	if err != nil {
		return err
	}
	f, err := x.Client.Move(ctx, a.FileID, parent, path.Base(a.NewRelPath))
	if err != nil {
		return err
	}
	x.renamePathIDs(a.RelPath, a.NewRelPath)
	return x.DB.CompleteOp(opID, func(tx *sql.Tx) error {
		if err := statedb.RenameItemPath(tx, a.RelPath, a.NewRelPath); err != nil {
			return err
		}
		// Refresh the moved row's version and local stat: the local file
		// already sits at the destination (the move was local-driven).
		info, statErr := os.Stat(x.abs(a.NewRelPath))
		if statErr != nil {
			return statErr
		}
		return statedb.UpsertItem(tx, statedb.Item{
			DriveFileID: f.ID, RelPath: a.NewRelPath, Size: info.Size(),
			ContentMD5: a.MD5, LocalMtimeNS: info.ModTime().UnixNano(),
			DriveMD5: f.MD5, DriveVersion: f.Version,
		})
	})
}

func (x *Executor) conflictBackup(opID int64, a reconcile.Action) error {
	if err := os.Rename(x.abs(a.RelPath), x.abs(a.NewRelPath)); err != nil {
		return err
	}
	fsyncDir(filepath.Dir(x.abs(a.RelPath)))
	// No row change: the canonical path's row is replaced by the download;
	// the backup is uploaded as a brand-new file.
	return x.DB.CompleteOp(opID, nil)
}

func (x *Executor) upload(ctx context.Context, opID int64, a reconcile.Action) error {
	abs := x.abs(a.RelPath)
	pre, err := os.Stat(abs)
	if err != nil {
		return err
	}
	hash, err := hashFile(abs)
	if err != nil {
		return err
	}
	f, err := os.Open(abs)
	if err != nil {
		return err
	}
	defer f.Close()
	parent, err := x.parentID(a.RelPath)
	if err != nil {
		return err
	}
	remote, err := x.Client.Upload(ctx, parent, path.Base(a.RelPath), f, pre.Size())
	if err != nil {
		return err
	}
	return x.commitUpload(opID, a.RelPath, abs, hash, pre, remote)
}

func (x *Executor) updateRemote(ctx context.Context, opID int64, a reconcile.Action) error {
	abs := x.abs(a.RelPath)
	pre, err := os.Stat(abs)
	if err != nil {
		return err
	}
	hash, err := hashFile(abs)
	if err != nil {
		return err
	}
	f, err := os.Open(abs)
	if err != nil {
		return err
	}
	defer f.Close()
	remote, err := x.Client.Update(ctx, a.FileID, f, pre.Size())
	if err != nil {
		return err
	}
	return x.commitUpload(opID, a.RelPath, abs, hash, pre, remote)
}

// commitUpload verifies Drive stored what we hashed and commits the row.
// If the file changed mid-upload we commit the pre-upload stat so the next
// scan sees it dirty and re-uploads.
func (x *Executor) commitUpload(opID int64, rel, abs, hash string, pre os.FileInfo, remote driveclient.File) error {
	if err := checkpoint(CPUploadBeforeCommit); err != nil {
		return err
	}
	if remote.MD5 != hash {
		return fmt.Errorf("drive reported md5 %s but local content hashed %s (file changed mid-upload?); will retry next run", remote.MD5, hash)
	}
	x.setPathID(rel, remote.ID)
	return x.DB.CompleteOp(opID, func(tx *sql.Tx) error {
		return statedb.UpsertItem(tx, statedb.Item{
			DriveFileID: remote.ID, RelPath: rel, Size: pre.Size(), ContentMD5: hash,
			LocalMtimeNS: pre.ModTime().UnixNano(), DriveMD5: remote.MD5, DriveVersion: remote.Version,
		})
	})
}

func (x *Executor) download(ctx context.Context, opID int64, a reconcile.Action) error {
	target := x.abs(a.RelPath)
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, names.TempPrefix+"*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after successful rename

	body, err := x.Client.Download(ctx, a.FileID)
	if err != nil {
		tmp.Close()
		return err
	}
	h := md5.New()
	_, err = io.Copy(io.MultiWriter(tmp, h), body)
	body.Close()
	if err != nil {
		tmp.Close()
		return err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if a.MD5 != "" && sum != a.MD5 {
		tmp.Close()
		return fmt.Errorf("downloaded content md5 %s does not match expected %s (changed remotely mid-sync?); will retry next run", sum, a.MD5)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := checkpoint(CPDownloadTempWritten); err != nil {
		return err
	}
	// Spec §7 overwrite guard: the atomic replace may only land on what the
	// plan assumed is here (the scanned file, or nothing). A local write
	// racing this cycle wins it; the refused download is replanned — and by
	// then it is a local change, so the decision table conflicts it instead
	// of overwriting. A sub-microsecond race between this stat and the
	// rename remains and is accepted.
	cur, statErr := os.Lstat(target)
	switch {
	case statErr == nil:
		if !a.LocalExists {
			return fmt.Errorf("a file appeared at the target after the scan; leaving it alone, replanning next cycle")
		}
		if !cur.Mode().IsRegular() || cur.Size() != a.LocalSize || cur.ModTime().UnixNano() != a.LocalMtimeNS {
			return fmt.Errorf("target changed after the scan (size %d mtime %d, scanned size %d mtime %d); leaving the local file alone, replanning next cycle",
				cur.Size(), cur.ModTime().UnixNano(), a.LocalSize, a.LocalMtimeNS)
		}
	case os.IsNotExist(statErr):
		if a.LocalExists {
			return fmt.Errorf("target disappeared after the scan; replanning next cycle")
		}
	default:
		return statErr
	}
	if err := os.Rename(tmpName, target); err != nil {
		return err
	}
	fsyncDir(dir)
	if err := checkpoint(CPDownloadBeforeCommit); err != nil {
		return err
	}
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	x.setPathID(a.RelPath, a.FileID)
	return x.DB.CompleteOp(opID, func(tx *sql.Tx) error {
		return statedb.UpsertItem(tx, statedb.Item{
			DriveFileID: a.FileID, RelPath: a.RelPath, Size: info.Size(), ContentMD5: sum,
			LocalMtimeNS: info.ModTime().UnixNano(), DriveMD5: sum, DriveVersion: a.Version,
		})
	})
}

func (x *Executor) record(opID int64, a reconcile.Action) error {
	item := statedb.Item{
		DriveFileID: a.FileID, RelPath: a.RelPath, IsDir: a.IsDir,
		Size: a.Size, ContentMD5: a.MD5, DriveMD5: a.MD5, DriveVersion: a.Version,
	}
	if !a.IsDir {
		info, err := os.Stat(x.abs(a.RelPath))
		if err != nil {
			return err
		}
		item.Size = info.Size()
		item.LocalMtimeNS = info.ModTime().UnixNano()
	}
	x.setPathID(a.RelPath, a.FileID)
	return x.DB.CompleteOp(opID, func(tx *sql.Tx) error {
		return statedb.UpsertItem(tx, item)
	})
}

func (x *Executor) trashRemote(ctx context.Context, opID int64, a reconcile.Action) error {
	if err := x.Client.Trash(ctx, a.FileID); err != nil {
		return err
	}
	return x.DB.CompleteOp(opID, func(tx *sql.Tx) error {
		return statedb.DeleteItemByID(tx, a.FileID)
	})
}

func (x *Executor) quarantineLocal(opID int64, a reconcile.Action) error {
	abs := x.abs(a.RelPath)
	if a.IsDir {
		// Children were quarantined first (bottom-up); the dir should be
		// empty now. A non-empty dir means something unexpected survives —
		// leave it and fail the op rather than lose data.
		if err := os.Remove(abs); err != nil {
			return fmt.Errorf("remove dir (should be empty after children): %w", err)
		}
	} else {
		dest := filepath.Join(x.QuarantineDir, time.Now().Format("2006-01-02"), filepath.FromSlash(a.RelPath))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := moveFile(abs, dest); err != nil {
			return err
		}
	}
	return x.DB.CompleteOp(opID, func(tx *sql.Tx) error {
		return statedb.DeleteItemByID(tx, a.FileID)
	})
}

func (x *Executor) renamePathIDs(from, to string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	prefix := from + "/"
	updated := map[string]string{}
	for p, id := range x.pathIDs {
		switch {
		case p == from:
			updated[to] = id
		case strings.HasPrefix(p, prefix):
			updated[to+"/"+strings.TrimPrefix(p, prefix)] = id
		default:
			updated[p] = id
		}
	}
	x.pathIDs = updated
}

// moveFile renames, falling back to copy+remove across filesystems (the
// quarantine dir may live on a different volume than the sync dir).
func moveFile(from, to string) error {
	if err := os.Rename(from, to); err == nil {
		return nil
	}
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	if err := dst.Sync(); err != nil {
		dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	return os.Remove(from)
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// fsyncDir makes a directory entry durable on POSIX; no-op on Windows.
func fsyncDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	if f, err := os.Open(dir); err == nil {
		f.Sync()
		f.Close()
	}
}
