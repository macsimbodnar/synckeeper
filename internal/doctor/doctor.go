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
	"github.com/macsimbodnar/synckeeper/internal/scanner"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
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
}

// Healthy reports whether nothing needs attention.
func (r *Report) Healthy() bool {
	return len(r.MissingLocal) == 0 && len(r.LocalModified) == 0 &&
		len(r.UntrackedLocal) == 0 && len(r.RemoteOnly) == 0 &&
		len(r.RemoteMissing) == 0 && r.StaleOps == 0 && len(r.OrphanTemps) == 0 &&
		len(r.Notes) == 0
}

// Check is read-only: it compares DB vs disk vs Drive and reports every
// divergence. With no page token (lost DB) it degrades to what it can see.
func (d *Doctor) Check(ctx context.Context) (*Report, error) {
	rep := &Report{}

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
	local, _, err := scanner.Scan(d.SyncDir, base, d.Cfg.Engine.Ignore)
	if err != nil {
		return nil, fmt.Errorf("scan local dir: %w", err)
	}

	// DB vs disk.
	for p, b := range base {
		loc, ok := local[p]
		switch {
		case !ok:
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
		remote, _, err := remotedelta.Snapshot(d.DB, rootID, d.Cfg.Engine.Ignore)
		if err != nil {
			return nil, err
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
	folder, err := driveclient.FindOrCreateFolder(ctx, d.Client, "root", d.Cfg.Drive.FolderName)
	if err != nil {
		return nil, fmt.Errorf("find or create Drive folder %q: %w", d.Cfg.Drive.FolderName, err)
	}
	if err := d.DB.SetMeta(statedb.MetaRootFolderID, folder.ID); err != nil {
		return nil, err
	}
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
	remote, _, err := remotedelta.Snapshot(d.DB, folder.ID, d.Cfg.Engine.Ignore)
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

func findOrphanTemps(syncDir string) []string {
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
	for _, s := range [][]string{r.MissingLocal, r.LocalModified, r.UntrackedLocal, r.RemoteOnly, r.RemoteMissing, r.OrphanTemps} {
		sort.Strings(s)
	}
}
