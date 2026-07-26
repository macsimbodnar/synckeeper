// Package engine orchestrates one sync cycle: guards, local scan, remote
// delta, reconcile, execute. It is shared by `sync` now and `watch` in
// phase 3, and is driven against the fake Drive in scenario tests.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/config"
	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/executor"
	"github.com/macsimbodnar/synckeeper/internal/guards"
	"github.com/macsimbodnar/synckeeper/internal/names"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/remotedelta"
	"github.com/macsimbodnar/synckeeper/internal/scanner"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
	"github.com/macsimbodnar/synckeeper/internal/trash"
)

// MetaLastSkipped stores the previous run's skip report for `status`.
const MetaLastSkipped = "last_skipped"

// Engine holds everything a sync cycle needs.
type Engine struct {
	DB            *statedb.DB
	Client        driveclient.Client
	Cfg           config.Config
	SyncDir       string
	QuarantineDir string
	RootID        string

	// Trash receives content deleted in Drive (W13). nil means the
	// platform's own bin; tests inject a fake one.
	Trash trash.Trasher

	caseFold *bool // lazily probed once; sync cycles are serialized
	normFold *bool
}

// defaultTrash is the bin used when Engine.Trash is nil. A var so the suite's
// TestMain can pin it away from the developer's own trash.
var defaultTrash trash.Trasher = trash.OS()

func (e *Engine) trasher() trash.Trasher {
	if e.Trash != nil {
		return e.Trash
	}
	return defaultTrash
}

// caseInsensitive reports (and caches) whether the sync dir folds case, so
// remote siblings differing only by case are collapsed instead of colliding
// on disk.
func (e *Engine) caseInsensitive() bool {
	if e.caseFold == nil {
		v := names.CaseInsensitiveFS(e.SyncDir)
		e.caseFold = &v
	}
	return *e.caseFold
}

// normInsensitive reports (and caches) whether the sync dir folds Unicode
// normalization, so remote siblings differing only by NFC/NFD are collapsed
// instead of colliding on disk.
func (e *Engine) normInsensitive() bool {
	if e.normFold == nil {
		v := names.NormalizationInsensitiveFS(e.SyncDir)
		e.normFold = &v
	}
	return *e.normFold
}

// Options tweak one run.
type Options struct {
	DryRun         bool
	ConfirmDeletes bool
	// DeferMassDelete makes a tripped mass-delete guard strip only the
	// delete-class actions and execute everything else, recording the block in
	// the Result (spec §8.1 — the daemon "keeps syncing everything else").
	// The interactive one-shot leaves this false so the guard aborts the cycle
	// with the --confirm-deletes hint (spec §6).
	DeferMassDelete bool
}

// Result is what one run did (or, dry-run, would do).
type Result struct {
	Plan         []reconcile.Action
	Skips        []reconcile.Skip
	Executed     int
	Failed       int
	Errors       []string
	GuardBlocked bool // a mass-delete guard tripped and its deletes were deferred
	GuardReason  string

	// TrashAvailable reports where this cycle's remote-initiated deletions
	// went: the system bin, or the quarantine when the platform has no bin
	// (W13). `activity` names the destination it actually used.
	TrashAvailable bool
}

// Sync runs one full cycle.
func (e *Engine) Sync(ctx context.Context, opts Options) (*Result, error) {
	baseItems, err := e.DB.AllItems()
	if err != nil {
		return nil, err
	}
	if err := guards.CheckSyncDir(e.SyncDir, len(baseItems)); err != nil {
		return nil, err
	}
	if err := executor.CleanStaleState(e.DB, e.SyncDir); err != nil {
		return nil, err
	}

	if err := remotedelta.Refresh(ctx, e.Client, e.DB, e.RootID); err != nil {
		return nil, fmt.Errorf("refresh remote state: %w", err)
	}
	remote, remoteSkips, err := remotedelta.Snapshot(e.DB, e.RootID, e.Cfg.Engine.Ignore, e.caseInsensitive(), e.normInsensitive())
	if err != nil {
		return nil, err
	}

	base := map[string]reconcile.BaseItem{}
	for _, it := range baseItems {
		base[it.RelPath] = reconcile.BaseItem{
			FileID: it.DriveFileID, IsDir: it.IsDir, Size: it.Size, MD5: it.ContentMD5,
			MtimeNS: it.LocalMtimeNS, DriveMD5: it.DriveMD5, DriveVersion: it.DriveVersion,
		}
	}
	local, localSkips, err := scanner.Scan(e.SyncDir, base, e.Cfg.Engine.Ignore)
	if err != nil {
		return nil, fmt.Errorf("scan local dir: %w", err)
	}

	// Ids collapsed out of the snapshot by a duplicate or fold collision are
	// alive on Drive; reconcile holds their baseline rows harmless (R19).
	shadowed := expandShadowed(baseItems, remoteSkips)
	plan, planSkips := reconcile.Plan(reconcile.Input{
		Base:           base,
		Local:          local,
		Remote:         remote,
		Machine:        e.Cfg.Engine.MachineName,
		Now:            nowFunc(),
		CaseFold:       e.caseInsensitive(),
		NormFold:       e.normInsensitive(),
		ShadowedRemote: shadowed,
	})
	res := &Result{Plan: plan}
	res.Skips = append(res.Skips, remoteSkips...)
	res.Skips = append(res.Skips, localSkips...)
	res.Skips = append(res.Skips, planSkips...)

	// The guard's universe is tracked files — containers are excluded on
	// both sides of the fraction (spec §6, R10).
	trackedFiles := 0
	for _, it := range baseItems {
		if !it.IsDir {
			trackedFiles++
		}
	}
	guardErr := guards.CheckMassDelete(plan, trackedFiles, e.Cfg.Engine.MassDeleteThreshold, opts.ConfirmDeletes)

	// The collapse runs AFTER the guard has counted the plan and never
	// before (W13-T2): the guard counts delete-class files, and a folder
	// collapsed into one action would make it count zero exactly when it
	// matters most. Only worth doing when there is a bin to receive the
	// folder — without one the quarantine takes it item by item, as before.
	res.TrashAvailable = e.trasher().Available()
	if res.TrashAvailable {
		plan = reconcile.CollapseDirDeletes(plan)
		res.Plan = plan
	}

	if guardErr != nil {
		if !opts.DeferMassDelete {
			return res, guardErr // interactive one-shot: abort with the hint (spec §6)
		}
		// Daemon: never self-confirm and never block the whole cycle. Strip the
		// delete-class actions, record the block for `status`, and keep syncing
		// everything else (spec §8.1). res.Plan keeps the full plan so the
		// deferred deletes stay visible; the executor runs the rest.
		res.GuardBlocked = true
		res.GuardReason = guardErr.Error()
		plan = withoutDeletes(plan)
	}
	if opts.DryRun {
		return res, nil
	}

	x := &executor.Executor{
		DB: e.DB, Client: e.Client, SyncDir: e.SyncDir,
		QuarantineDir: e.QuarantineDir, RootID: e.RootID,
		Ignore: e.Cfg.Engine.Ignore, Trash: e.trasher(),
	}
	sum, err := x.Apply(ctx, plan)
	if err != nil {
		return res, err
	}
	res.Executed, res.Failed, res.Errors = sum.Executed, sum.Failed, sum.Errors

	if skipJSON, err := json.Marshal(res.Skips); err == nil {
		e.DB.SetMeta(MetaLastSkipped, string(skipJSON))
	}
	if res.Failed == 0 {
		purgeQuarantine(e.QuarantineDir, e.Cfg.Engine.QuarantineRetentionDays)
	}
	return res, nil
}

// expandShadowed returns the baseline file ids to hold harmless this cycle:
// the ids Snapshot skipped as shadowed (a duplicate or fold-colliding sibling
// that lost "first by id"), PLUS every tracked descendant of a shadowed
// FOLDER. Snapshot never walks a shadowed folder's subtree, so those rows are
// absent from the remote snapshot; without this they read as remote-deleted
// and get quarantined — breaking the guarantee that a name collision never
// sends content to quarantine (spec §5).
func expandShadowed(baseItems []statedb.Item, skips []reconcile.Skip) map[string]bool {
	shadowed := map[string]bool{}
	var prefixes []string
	for _, s := range skips {
		if s.FileID == "" {
			continue
		}
		shadowed[s.FileID] = true
		prefixes = append(prefixes, s.RelPath+"/")
	}
	for _, it := range baseItems {
		for _, pre := range prefixes {
			if strings.HasPrefix(it.RelPath, pre) {
				shadowed[it.DriveFileID] = true
				break
			}
		}
	}
	return shadowed
}

// withoutDeletes returns the plan minus the delete-class actions (the daemon's
// deferred mass-delete path). Deletes are the last, bottom-up stage and nothing
// depends on one completing, so the remaining prefix stays self-consistent.
func withoutDeletes(plan []reconcile.Action) []reconcile.Action {
	out := make([]reconcile.Action, 0, len(plan))
	for _, a := range plan {
		if a.Type == reconcile.TrashRemote || a.Type == reconcile.QuarantineLocal {
			continue
		}
		out = append(out, a)
	}
	return out
}

// purgeQuarantine removes dated quarantine folders older than the retention
// window. Runs only after a fully successful sync; failures are logged, not
// fatal (quarantine is a rescue path, purging it is best-effort).
func purgeQuarantine(dir string, retentionDays int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // no quarantine yet
	}
	cutoff := nowFunc().AddDate(0, 0, -retentionDays)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		day, err := time.Parse("2006-01-02", e.Name())
		if err != nil {
			continue // not one of ours
		}
		if day.Before(cutoff) {
			path := filepath.Join(dir, e.Name())
			if err := os.RemoveAll(path); err != nil {
				slog.Warn("purge quarantine entry", "path", path, "err", err)
			} else {
				slog.Info("purged expired quarantine entries", "path", path)
			}
		}
	}
}

// nowFunc is swappable in tests that need deterministic conflict names.
var nowFunc = defaultNow

func defaultNow() (t time.Time) { return time.Now() }
