// Package doctor cross-checks the three sources of truth — state DB, local
// disk, Drive — and can rebuild a lost baseline. Repair only ever ADDS
// baseline rows (adopting md5-equal pairs) and cleans synckeeper's own
// artifacts; it never deletes user data on either side. Divergence left
// after repair is normal sync work and is listed in the report.
package doctor

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/macsimbodnar/synckeeper/internal/config"
	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/names"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/remotedelta"
	"github.com/macsimbodnar/synckeeper/internal/root"
	"github.com/macsimbodnar/synckeeper/internal/scanner"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
	"github.com/macsimbodnar/synckeeper/internal/trash"
)

// Doctor bundles what checks and repairs need.
type Doctor struct {
	DB      *statedb.DB
	Client  driveclient.Client
	Cfg     config.Config
	SyncDir string
}

// Report is the outcome of a check (and, after --repair, of the repair).
type Report struct {
	RootFolderID   string
	TrackedItems   int
	MissingLocal   []string // row exists, local file gone
	LocalModified  []string // local content differs from the row
	UntrackedLocal []string // local file with no row
	RemoteOnly     []string // remote file with no row
	RemoteMissing  []string // row exists, remote gone
	StaleOps       int
	OrphanTemps    []string
	Adopted        int // rows written by repair
	Notes          []string

	// DriveDuplicates are folders on Drive holding two items under one name
	// (Drive allows it, a filesystem does not). Synckeeper keeps the first by
	// file id and skips the rest, so the shadowed item is invisible on every
	// machine — including, after W17's crash window, a copy of your own file.
	// Reported only: resolving it means choosing a winner in the Drive web
	// UI, which is a person's decision, and `--repair` only ever adds (§12).
	DriveDuplicates []string

	// HeldLocal are rows whose local path is on disk but was not read by the
	// scan: a symlink or non-regular file, a file or directory it may not
	// read. A sync holds them on both sides instead of reading "absent" as a
	// local deletion, so they are not MissingLocal (S026).
	HeldLocal []HeldPath

	// SystemBin is where a deletion arriving from Drive lands on this
	// machine, and whether that destination is the user's own bin (W14-M2).
	// Reported, never a fault: a platform without a bin is not broken, it is
	// the platform where a mass deletion still asks for confirmation.
	SystemBin   string
	SystemBinOK bool
}

// HeldPath is a row whose local path is on disk but was not read by the scan.
type HeldPath struct {
	RelPath string
	Reason  string
}

// Healthy reports whether nothing needs attention.
func (r *Report) Healthy() bool {
	return len(r.MissingLocal) == 0 && len(r.HeldLocal) == 0 && len(r.LocalModified) == 0 &&
		len(r.UntrackedLocal) == 0 && len(r.RemoteOnly) == 0 &&
		len(r.RemoteMissing) == 0 && r.StaleOps == 0 && len(r.OrphanTemps) == 0 &&
		len(r.DriveDuplicates) == 0 && len(r.Notes) == 0
}

// Check is read-only: it compares DB vs disk vs Drive and reports every
// divergence. With no page token (lost DB) it degrades to what it can see.
func (d *Doctor) Check(ctx context.Context) (*Report, error) {
	rep := &Report{SystemBin: trash.Describe(), SystemBinOK: trash.Available()}

	rootID, err := d.DB.GetMeta(statedb.MetaRootFolderID)
	if errors.Is(err, statedb.ErrNotFound) {
		rep.Notes = append(rep.Notes, "state DB has no root folder id (new or lost DB); run `doctor --repair` to rebuild the baseline")
	} else if err != nil {
		return nil, err
	}
	rep.RootFolderID = rootID

	items, err := d.DB.AllItems()
	if err != nil {
		return nil, err
	}
	rep.TrackedItems = len(items)

	stale, err := d.DB.StaleOps()
	if err != nil {
		return nil, err
	}
	rep.StaleOps = len(stale)
	rep.OrphanTemps = findOrphanTemps(d.SyncDir)

	base := map[string]reconcile.BaseItem{}
	for _, it := range items {
		base[it.RelPath] = reconcile.BaseItem{
			FileID: it.DriveFileID, IsDir: it.IsDir, Size: it.Size, MD5: it.ContentMD5,
			MtimeNS: it.LocalMtimeNS, DriveMD5: it.DriveMD5, DriveVersion: it.DriveVersion,
		}
	}
	local, localSkips, err := scanner.Scan(d.SyncDir, base, d.Cfg.Engine.Ignore)
	if err != nil {
		return nil, fmt.Errorf("scan local dir: %w", err)
	}
	heldWhy := heldBy(localSkips, names.CaseInsensitiveFS(d.SyncDir), names.NormalizationInsensitiveFS(d.SyncDir))

	// DB vs disk.
	for p, b := range base {
		loc, ok := local[p]
		switch {
		case !ok:
			if why, held := heldWhy(p); held {
				rep.HeldLocal = append(rep.HeldLocal, HeldPath{RelPath: p, Reason: why})
				continue
			}
			rep.MissingLocal = append(rep.MissingLocal, p)
		case !b.IsDir && !loc.IsDir && loc.MD5 != b.MD5:
			rep.LocalModified = append(rep.LocalModified, p)
		}
	}
	for p := range local {
		if _, ok := base[p]; !ok {
			rep.UntrackedLocal = append(rep.UntrackedLocal, p)
		}
	}

	// DB vs Drive, when we have enough metadata to look.
	if rootID != "" {
		if _, err := d.DB.GetMeta(statedb.MetaPageToken); err == nil {
			if err := remotedelta.Refresh(ctx, d.Client, d.DB, rootID); err != nil {
				rep.Notes = append(rep.Notes, fmt.Sprintf("could not refresh remote state (offline?): %v — comparing against cached state", err))
			}
		} else {
			rep.Notes = append(rep.Notes, "no changes page token; remote comparison uses cached state — run `doctor --repair`")
		}
		remote, skips, err := remotedelta.Snapshot(d.DB, rootID, d.Cfg.Engine.Ignore, names.CaseInsensitiveFS(d.SyncDir), names.NormalizationInsensitiveFS(d.SyncDir))
		if err != nil {
			return nil, err
		}
		for _, sk := range skips {
			if sk.Duplicate {
				rep.DriveDuplicates = append(rep.DriveDuplicates, sk.RelPath)
			}
		}
		remoteIDs := map[string]bool{}
		for _, r := range remote {
			remoteIDs[r.FileID] = true
		}
		for p, b := range base {
			if !remoteIDs[b.FileID] {
				rep.RemoteMissing = append(rep.RemoteMissing, p)
			}
		}
		baseIDs := map[string]bool{}
		for _, b := range base {
			baseIDs[b.FileID] = true
		}
		for p, r := range remote {
			if !baseIDs[r.FileID] {
				rep.RemoteOnly = append(rep.RemoteOnly, p)
			}
		}
	}

	sortAll(rep)
	return rep, nil
}

// Repair rebuilds what can be rebuilt safely, then re-checks:
//   - restores root folder id / machine id metadata (finding the Drive
//     folder by its configured name),
//   - rebuilds the remote cache with a forced full walk and a fresh token,
//   - adopts every path whose local and remote content already match
//     (md5-equal files, dirs present on both sides) into the baseline,
//   - clears stale pending ops and orphan temp files.
//
// It never trashes, quarantines, or overwrites anything: after a lost DB,
// unmatched local files become uploads and unmatched remote files become
// downloads on the next sync — deletions cannot be produced by adoption.
func (d *Doctor) Repair(ctx context.Context) (*Report, error) {
	// Id-first (W18-A). This call site was one of the two carriers of F1: it
	// resolved the folder by NAME through FindOrCreateFolder, so a folder
	// renamed in the Drive web UI made "repair" create a new empty one, repoint
	// at it, and leave every baseline row — after which the next ordinary sync
	// moved the user's whole tree to the system bin. root.Resolve keeps the
	// stored id whatever the folder is now called, and resets the baseline in
	// the same transaction on the one path that really does change identity.
	res, err := root.Resolve(ctx, d.Client, d.DB, d.Cfg.Drive.FolderName)
	if err != nil {
		return nil, err
	}
	folder := driveclient.File{ID: res.ID, Name: res.Name}
	if _, err := d.DB.GetMeta(statedb.MetaMachineID); errors.Is(err, statedb.ErrNotFound) {
		id := make([]byte, 8)
		if _, err := rand.Read(id); err != nil {
			return nil, err
		}
		if err := d.DB.SetMeta(statedb.MetaMachineID, hex.EncodeToString(id)); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}

	if err := remotedelta.ForceFullWalk(ctx, d.Client, d.DB, folder.ID); err != nil {
		return nil, fmt.Errorf("rebuild remote cache: %w", err)
	}
	remote, _, err := remotedelta.Snapshot(d.DB, folder.ID, d.Cfg.Engine.Ignore, names.CaseInsensitiveFS(d.SyncDir), names.NormalizationInsensitiveFS(d.SyncDir))
	if err != nil {
		return nil, err
	}

	items, err := d.DB.AllItems()
	if err != nil {
		return nil, err
	}
	base := map[string]reconcile.BaseItem{}
	for _, it := range items {
		base[it.RelPath] = reconcile.BaseItem{
			FileID: it.DriveFileID, IsDir: it.IsDir, Size: it.Size, MD5: it.ContentMD5,
			MtimeNS: it.LocalMtimeNS, DriveMD5: it.DriveMD5, DriveVersion: it.DriveVersion,
		}
	}
	local, _, err := scanner.Scan(d.SyncDir, base, d.Cfg.Engine.Ignore)
	if err != nil {
		return nil, fmt.Errorf("scan local dir: %w", err)
	}

	adopted := 0
	for p, r := range remote {
		loc, ok := local[p]
		if !ok || loc.IsDir != r.IsDir {
			continue
		}
		if !r.IsDir && loc.MD5 != r.MD5 {
			continue
		}
		err := d.DB.Tx(func(tx *sql.Tx) error {
			return statedb.UpsertItem(tx, statedb.Item{
				DriveFileID: r.FileID, RelPath: p, IsDir: r.IsDir, Size: loc.Size,
				ContentMD5: loc.MD5, LocalMtimeNS: loc.MtimeNS,
				DriveMD5: r.MD5, DriveVersion: r.Version,
			})
		})
		if err != nil {
			return nil, err
		}
		adopted++
	}

	if err := d.DB.ClearOps(); err != nil {
		return nil, err
	}
	for _, tmp := range findOrphanTemps(d.SyncDir) {
		os.Remove(filepath.Join(d.SyncDir, filepath.FromSlash(tmp)))
	}

	rep, err := d.Check(ctx)
	if err != nil {
		return nil, err
	}
	rep.Adopted = adopted
	return rep, nil
}

// heldBy returns, for a row absent from the local snapshot, why the engine
// holds it rather than reading it as deleted. It mirrors what a sync feeds the
// planner — engine.unscannedPaths matched like reconcile's fold-aware path
// holds (at or above the row), and engine.expandUnreadable's exact prefix —
// which are unexported there. Holds the planner infers from its move detection
// (a row renamed into an unreadable directory, or matched by stat to an
// unreadable file) are not reproduced: doctor detects no moves.
func heldBy(skips []reconcile.Skip, caseFold, normFold bool) func(string) (string, bool) {
	fold := func(p string) string {
		segs := strings.Split(p, "/")
		for i, s := range segs {
			segs[i] = names.FoldKey(s, caseFold, normFold)
		}
		return strings.Join(segs, "/")
	}
	unscanned := map[string]reconcile.Skip{} // folded path → its skip
	var unreadable []reconcile.Skip
	for _, s := range skips {
		switch {
		case s.Unscanned:
			unscanned[fold(s.RelPath)] = s
		case s.Unreadable:
			unreadable = append(unreadable, s)
		}
	}
	return func(p string) (string, bool) {
		fp := fold(p)
		for q := fp; len(unscanned) > 0; {
			if s, ok := unscanned[q]; ok {
				if q == fp {
					return s.Reason, true
				}
				return "under " + s.RelPath + " (" + s.Reason + ")", true
			}
			i := strings.LastIndexByte(q, '/')
			if i < 0 {
				break
			}
			q = q[:i]
		}
		for _, s := range unreadable {
			if strings.HasPrefix(p, s.RelPath+"/") {
				return "under " + s.RelPath + " (" + s.Reason + ")", true
			}
		}
		return "", false
	}
}

func findOrphanTemps(syncDir string) []string {
	// A walk does not descend a symlinked root (S007). Unresolvable means the
	// dir is missing; the walk below then finds nothing, as before.
	if r, err := filepath.EvalSymlinks(syncDir); err == nil {
		syncDir = r
	}
	var out []string
	filepath.WalkDir(syncDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasPrefix(d.Name(), names.TempPrefix) {
			if rel, err := filepath.Rel(syncDir, p); err == nil {
				out = append(out, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	return out
}

func sortAll(r *Report) {
	for _, s := range [][]string{r.MissingLocal, r.LocalModified, r.UntrackedLocal, r.RemoteOnly, r.RemoteMissing, r.OrphanTemps, r.DriveDuplicates} {
		sort.Strings(s)
	}
	sort.Slice(r.HeldLocal, func(i, j int) bool { return r.HeldLocal[i].RelPath < r.HeldLocal[j].RelPath })
}
