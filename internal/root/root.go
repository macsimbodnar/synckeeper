// Package root owns the identity of the Drive folder this machine syncs
// against: resolving it, noticing when it has been renamed, and recreating it
// when it is gone — without ever letting any of that read as a deletion (W18).
//
// The rule it exists to enforce is one line: **the Drive id is the identity,
// the configured name is only how we create the folder the first time.**
// Before W18 both `init --force` and `doctor --repair` resolved the root by
// NAME through driveclient.FindOrCreateFolder, which creates the folder when
// absent. A folder renamed in the Drive web UI therefore repointed
// root_folder_id at a brand-new empty folder while every baseline row
// survived, and the next ordinary cycle read the whole baseline as "deleted on
// Drive" and moved the user's tree to the system bin — reproduced at 3 files
// to 0, with the mass-delete guard silent because W14 turned it off wherever a
// system bin exists. See decisions.md 2026-07-31 and plan.md W18-A.
package root

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/remotedelta"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// Result describes the folder this machine syncs against and how it was found.
type Result struct {
	ID   string
	Name string

	// Created is set when no usable folder was found and a new one was made.
	// The baseline was reset in the same transaction that stored the new id,
	// so the coming cycle sees an empty baseline: every local file is a plain
	// upload, every remote file a plain download, and — the whole point —
	// spec §11 makes a delete structurally impossible.
	Created bool

	// Renamed is set when the stored id resolved fine but under a different
	// name than last recorded: the folder was renamed in the Drive web UI.
	// Nothing syncs differently; the observed name is stored so the read-only
	// views stop showing a stale one.
	Renamed bool
}

// Resolve returns the Drive folder to sync against, id-first.
//
//   - A stored root id that still resolves wins, whatever it is called now. A
//     name change is recorded, never acted on.
//   - A stored root id that is GONE (404, or in Drive's bin) means the folder
//     was deleted: a new one is created under the configured name, and the
//     baseline is reset in the same transaction so nothing is read as deleted.
//   - No stored id at all (a fresh machine, or a lost DB) is the only case
//     that resolves by name — there is nothing else to go on. This is what
//     makes an idempotent `init` a complete recovery for a lost DB.
//
// Errors that are not "gone" — a network failure, a permission problem — are
// returned, never treated as deletion. Misreading one would re-upload the
// whole tree against a folder that was there all along.
func Resolve(ctx context.Context, client driveclient.Client, db *statedb.DB, folderName string) (Result, error) {
	storedID, err := db.GetMeta(statedb.MetaRootFolderID)
	switch {
	case errors.Is(err, statedb.ErrNotFound), storedID == "":
		return resolveByName(ctx, client, db, folderName)
	case err != nil:
		return Result{}, err
	}

	f, err := client.Get(ctx, storedID)
	switch {
	case err == nil && f.IsDir() && !f.Trashed:
		return keep(db, f)
	case err == nil:
		// The id resolves to something we cannot sync into — binned, or no
		// longer a folder. Treated as gone, per the rule that a deleted root
		// is recreated rather than propagated.
		slog.Warn("the Drive folder this machine syncs with is no longer usable; creating a fresh one",
			"file_id", storedID, "trashed", f.Trashed, "is_folder", f.IsDir())
	case driveclient.IsNotFound(err):
		slog.Warn("the Drive folder this machine syncs with is gone; creating a fresh one",
			"file_id", storedID)
	default:
		return Result{}, fmt.Errorf("checking the Drive folder %s: %w", storedID, err)
	}
	return create(ctx, client, db, folderName)
}

// keep records a name change, if any, and returns the folder unchanged.
func keep(db *statedb.DB, f driveclient.File) (Result, error) {
	res := Result{ID: f.ID, Name: f.Name}
	recorded, err := db.GetMeta(statedb.MetaRootFolderName)
	if err != nil && !errors.Is(err, statedb.ErrNotFound) {
		return Result{}, err
	}
	if recorded == f.Name {
		return res, nil
	}
	if recorded != "" {
		res.Renamed = true
		slog.Info("the Drive folder was renamed; syncing with it unchanged (it is the same folder by id)",
			"was", recorded, "now", f.Name, "file_id", f.ID)
	}
	// config.toml is deliberately NOT rewritten: the TOML encoder re-emits the
	// whole document, so a hand-edited config would lose its comments and key
	// order. drive.folder_name keeps its one job — naming a folder we create.
	return res, db.SetMeta(statedb.MetaRootFolderName, f.Name)
}

// resolveByName is the no-stored-id path: a fresh machine, or a DB that lost
// its metadata. Name is all there is to go on, and creating the folder when it
// is absent is correct here — there is no baseline claiming otherwise.
func resolveByName(ctx context.Context, client driveclient.Client, db *statedb.DB, folderName string) (Result, error) {
	f, err := driveclient.FindOrCreateFolder(ctx, client, "root", folderName)
	if err != nil {
		return Result{}, fmt.Errorf("find or create Drive folder %q: %w", folderName, err)
	}
	// A baseline with no root id is a DB that lost its metadata, not a fresh
	// one. Its rows were built against a root we can no longer name, so they
	// cannot be trusted against this one: reset, and let the union merge
	// rebuild them. Nothing is deleted (spec §11).
	n, err := db.ItemCount()
	if err != nil {
		return Result{}, err
	}
	if n > 0 {
		slog.Warn("the state DB tracks files but has lost its Drive folder id; rebuilding the baseline by merging both sides (nothing is deleted)",
			"tracked", n, "folder", f.Name)
		if err := commitIdentity(db, f, true); err != nil {
			return Result{}, err
		}
		if err := remotedelta.ForceFullWalk(ctx, client, db, f.ID); err != nil {
			return Result{}, fmt.Errorf("rebuild remote mirror: %w", err)
		}
		return Result{ID: f.ID, Name: f.Name, Created: true}, nil
	}
	return Result{ID: f.ID, Name: f.Name}, commitIdentity(db, f, false)
}

// create makes a new folder under the configured name and hands the coming
// cycle an empty baseline.
func create(ctx context.Context, client driveclient.Client, db *statedb.DB, folderName string) (Result, error) {
	f, err := client.Mkdir(ctx, "root", folderName)
	if err != nil {
		return Result{}, fmt.Errorf("create Drive folder %q: %w", folderName, err)
	}
	if err := commitIdentity(db, f, true); err != nil {
		return Result{}, err
	}
	// After the identity is committed, never before: a crash here leaves the
	// new id stored, the baseline empty and MetaWalkDone retracted, which the
	// next cycle repairs by walking the new root from scratch.
	if err := remotedelta.ForceFullWalk(ctx, client, db, f.ID); err != nil {
		return Result{}, fmt.Errorf("build remote mirror: %w", err)
	}
	slog.Info("created a fresh Drive folder; this machine's files will be uploaded into it (nothing is deleted)",
		"folder", f.Name, "file_id", f.ID)
	return Result{ID: f.ID, Name: f.Name, Created: true}, nil
}

// commitIdentity stores the folder id and name — and, when the identity
// changed, resets the baseline in the SAME transaction. That atomicity is the
// entire safety property: a crash between the two would leave the old baseline
// pointing at the new folder, which is the bug this package exists to remove.
func commitIdentity(db *statedb.DB, f driveclient.File, changed bool) error {
	return db.Tx(func(tx *sql.Tx) error {
		if err := statedb.SetMetaTx(tx, statedb.MetaRootFolderID, f.ID); err != nil {
			return err
		}
		if err := statedb.SetMetaTx(tx, statedb.MetaRootFolderName, f.Name); err != nil {
			return err
		}
		if !changed {
			return nil
		}
		if err := statedb.ResetBaseline(tx); err != nil {
			return err
		}
		return statedb.DeleteMetaTx(tx, remotedelta.MetaWalkDone)
	})
}

// Name is the Drive folder's name for display: the one last observed, falling
// back to the configured name before the first resolve. Read-only commands use
// it so a folder renamed in the Drive web UI reads truthfully.
func Name(db *statedb.DB, configured string) string {
	if db == nil {
		return configured
	}
	if name, err := db.GetMeta(statedb.MetaRootFolderName); err == nil && name != "" {
		return name
	}
	return configured
}
