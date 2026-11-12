package executor

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// finishOwedCommits commits what an interrupted run did on disk but never
// recorded (S035, spec §4.6): a local rename (MoveLocal, file or folder) or a
// download's atomic replace done, its op still in progress — the process died
// between the two, or the commit's DB write failed. The journal names the op;
// the disk must show exactly what the op left there, or nothing is done and
// the replan meets the state as before.
//
// Was: discard-and-replan alone. The replan finds a renamed file or folder
// only where it still sits at Drive's path with the old one gone (R16, R25);
// once Drive moved it on, or a new file or folder took the old name here, the
// moved content read as a new local item next to a row whose file was
// "missing" — duplicates on every machine, a plan refused every cycle (§4.5),
// or no convergence.
//
// Evidence, per op. The executor journals a MoveLocal "applied" between its
// rename and its commit; for such an op the source may exist again (a new
// file or folder took the name). Without that mark (the mark's own write
// lost) the source must be gone.
//   - folder MoveLocal: the source is gone, the destination is a folder, the
//     baseline still has the folder's row at the source, and no row sits where
//     its rows would land;
//   - file MoveLocal: the source is gone, the destination is a regular file
//     with the row's size and its mtime or content (a rename keeps both), the
//     row with the op's id still at the source, no other row at the
//     destination;
//   - Download: the target holds the op's md5 (the download verified it before
//     the replace), and no other row sits there unless the download was
//     planned to replace it (pinned).
//
// A DB error fails the cycle, which retries (W18-H's rule: what may clear by
// itself is retried, never skipped).
func finishOwedCommits(db *statedb.DB, syncDir string, stale []statedb.PendingOp) error {
	for _, op := range stale {
		applied := op.State == statedb.OpApplied
		if !applied && op.State != statedb.OpInProgress {
			continue
		}
		if op.Type != string(reconcile.MoveLocal) && op.Type != string(reconcile.Download) {
			continue
		}
		var a reconcile.Action
		if err := json.Unmarshal([]byte(op.Payload), &a); err != nil {
			continue
		}
		items, err := db.AllItems()
		if err != nil {
			return err
		}
		byPath := map[string]statedb.Item{}
		for _, it := range items {
			byPath[it.RelPath] = it
		}
		abs := func(rel string) string { return filepath.Join(syncDir, filepath.FromSlash(rel)) }
		var commit func(tx *sql.Tx) error
		switch a.Type {
		case reconcile.MoveLocal:
			if a.NewRelPath == "" || a.NewRelPath == a.RelPath {
				continue
			}
			// A case-only rename cut between its two steps (S038): finished
			// here, then judged like any other.
			if _, err := finishCaseRename(abs(a.NewRelPath), a.IsDir); err != nil {
				return err
			}
			// Gone by exact spelling: after a case-only rename D → d on a
			// folding disk a stat of D still finds the folder (S038).
			if !applied && !goneExact(abs(a.RelPath)) {
				// Not journaled as done, and the source is still there: the
				// rename never ran, or failed — nothing owed.
				continue
			}
			dst, err := os.Lstat(abs(a.NewRelPath))
			if err != nil {
				continue
			}
			row, ok := byPath[a.RelPath]
			if !ok || row.IsDir != a.IsDir || row.IsDir != dst.IsDir() || !row.IsDir && !dst.Mode().IsRegular() {
				continue
			}
			if !moveLandsFree(items, a.RelPath, a.NewRelPath) {
				continue
			}
			if !a.IsDir {
				if a.FileID == "" || row.DriveFileID != a.FileID || dst.Size() != row.Size {
					continue
				}
				if dst.ModTime().UnixNano() != row.LocalMtimeNS {
					sum, err := hashFile(abs(a.NewRelPath))
					if err != nil || sum != row.ContentMD5 {
						continue
					}
				}
			}
			commit = func(tx *sql.Tx) error { return statedb.RenameItemPath(tx, a.RelPath, a.NewRelPath) }
		case reconcile.Download:
			if a.FileID == "" || a.MD5 == "" {
				continue
			}
			info, err := os.Lstat(abs(a.RelPath))
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if other, ok := byPath[a.RelPath]; ok && other.DriveFileID != a.FileID && !a.LocalExists {
				continue // the download expected the path empty: that row is not its to drop
			}
			sum, err := hashFile(abs(a.RelPath))
			if err != nil || sum != a.MD5 {
				continue
			}
			item := statedb.Item{DriveFileID: a.FileID, RelPath: a.RelPath, Size: info.Size(),
				ContentMD5: sum, LocalMtimeNS: info.ModTime().UnixNano(), DriveMD5: sum, DriveVersion: a.Version}
			commit = func(tx *sql.Tx) error { return statedb.UpsertItem(tx, item) }
		}
		if err := db.Tx(commit); err != nil {
			return err
		}
		slog.Info("an interrupted run did this on disk but never recorded it; recording it before replanning",
			"type", a.Type, "rel_path", a.RelPath, "new_rel_path", a.NewRelPath)
	}
	return nil
}

// moveLandsFree reports whether renaming the row at from (and, for a folder,
// every row beneath it) to to collides with no row elsewhere — rows are unique
// per path, and a collision means the baseline already says something else
// lives there.
func moveLandsFree(items []statedb.Item, from, to string) bool {
	moving := map[string]bool{}
	for _, it := range items {
		if it.RelPath == from || strings.HasPrefix(it.RelPath, from+"/") {
			moving[it.RelPath] = true
		}
	}
	for _, it := range items {
		if moving[it.RelPath] {
			continue
		}
		if it.RelPath == to || strings.HasPrefix(it.RelPath, to+"/") {
			for m := range moving {
				if to+strings.TrimPrefix(m, from) == it.RelPath {
					return false
				}
			}
		}
	}
	return true
}
