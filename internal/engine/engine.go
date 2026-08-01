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

	// PreferNewer hands the plain name in a both-new conflict to the side
	// edited last instead of always to Drive. Set by `init`'s merge alone
	// (spec §11, W18-E); every other caller leaves it false so steady-state
	// §4.2 stays clock-free.
	PreferNewer bool

	// Stage, when set, is told which stage this cycle is in — reporting only,
	// read by the daemon's `stat` answer (W15-U5). Nil is valid and costs
	// nothing; the engine's behaviour is identical either way.
	Stage *StageReporter
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

	// DeletedLocal / DeletedRemote are how many FILES the executed plan
	// removed from this machine and from Drive (a collapsed folder counts
	// the files inside it). LargeDeletion marks a cycle whose deletions
	// crossed the mass-delete threshold and ran anyway because they are
	// recoverable — it is reported loudly rather than blocked (W14-M3), so
	// the user finds out well inside the bin's retention window.
	DeletedLocal   int
	DeletedRemote  int
	LargeDeletion  bool
	QuarantineFell int // items that could not reach the bin and went to the quarantine
}

// Sync runs one full cycle.
func (e *Engine) Sync(ctx context.Context, opts Options) (*Result, error) {
	opts.Stage.set(StageStarting)
	defer opts.Stage.Reset() // a finished cycle must not look like a running one
	baseItems, err := e.DB.AllItems()
	if err != nil {
		return nil, err
	}
	// A missing sync folder is recreated, never read as a deletion (W18-D).
	// The baseline reset is what makes that safe rather than catastrophic:
	// without it the cycle sees every tracked file gone locally while Drive
	// still holds it, and plans TrashRemote for the lot — emptying the user's
	// Drive folder. With an empty baseline §11 makes a delete structurally
	// impossible and the tree simply downloads again.
	recreated, err := guards.EnsureSyncDir(e.SyncDir, len(baseItems))
	if err != nil {
		return nil, err
	}
	if recreated && len(baseItems) > 0 {
		if err := e.DB.Tx(statedb.ResetBaseline); err != nil {
			return nil, fmt.Errorf("reset baseline after recreating the sync dir: %w", err)
		}
		baseItems = nil
	}
	if err := executor.CleanStaleState(ctx, e.DB, e.Client, e.RootID, e.SyncDir); err != nil {
		return nil, err
	}

	// The network stage: the one that can hang for a long time and the reason a
	// stage indicator is worth having at all.
	opts.Stage.set(StageCheckingDriv)
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
	opts.Stage.set(StageScanning)
	local, localSkips, err := scanner.Scan(e.SyncDir, base, e.Cfg.Engine.Ignore)
	if err != nil {
		return nil, fmt.Errorf("scan local dir: %w", err)
	}

	// Ids collapsed out of the snapshot by a duplicate or fold collision are
	// alive on Drive; reconcile holds their baseline rows harmless (R19).
	opts.Stage.set(StagePlanning)
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
		PreferNewer:    opts.PreferNewer,
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
	// Where this cycle's deletions would land decides whether they need a
	// question at all (W14-M1): a bin the user can see needs none.
	res.TrashAvailable = e.trasher().Available()
	guardErr := guards.CheckMassDelete(plan, trackedFiles, e.Cfg.Engine.MassDeleteThreshold, opts.ConfirmDeletes, res.TrashAvailable)

	// The collapse runs AFTER the guard has counted the plan and never
	// before (W13-T2): the guard counts delete-class files, and a folder
	// collapsed into one action would make it count zero exactly when it
	// matters most. Only worth doing when there is a bin to receive the
	// folder — without one the quarantine takes it item by item, as before.
	if res.TrashAvailable {
		plan = reconcile.CollapseDirDeletes(plan, remote)
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
	// The plan's size is known here, so the total comes free; only a done-of-
	// total counter would need the executor, which is deliberately not
	// instrumented for a display (decisions.md 2026-07-28).
	opts.Stage.set(StageTransferring)
	opts.Stage.setActions(len(plan))
	sum, err := x.Apply(ctx, plan)
	if err != nil {
		return res, err
	}
	res.Executed, res.Failed, res.Errors = sum.Executed, sum.Failed, sum.Errors
	res.QuarantineFell = sum.QuarantineFallbacks
	e.reportDeletions(res, plan, trackedFiles)

	opts.Stage.set(StageFinishing)
	if skipJSON, err := json.Marshal(res.Skips); err == nil {
		e.DB.SetMeta(MetaLastSkipped, string(skipJSON))
	}
	if res.Failed == 0 {
		purgeQuarantine(e.QuarantineDir, e.Cfg.Engine.QuarantineRetentionDays)
	}
	return res, nil
}

// reportDeletions counts what the executed plan removed and, when that
// crossed the mass-delete threshold, says so loudly (W14-M3). Since W14 a
// large deletion is no longer a question — every item is one gesture away in
// a bin — but it must still be impossible to miss, because the bins do not
// keep their contents forever. Skipped when actions failed: with a partial
// cycle we cannot claim what was removed.
func (e *Engine) reportDeletions(res *Result, executed []reconcile.Action, trackedFiles int) {
	if res.Failed > 0 {
		return
	}
	for _, a := range executed {
		n := 1
		if a.IsDir {
			n = a.SubtreeFiles
		}
		switch a.Type {
		case reconcile.QuarantineLocal:
			res.DeletedLocal += n
		case reconcile.TrashRemote:
			res.DeletedRemote += n
		}
	}
	deleted := max(res.DeletedLocal, res.DeletedRemote)
	if trackedFiles == 0 || deleted <= 10 ||
		float64(deleted)/float64(trackedFiles) <= e.Cfg.Engine.MassDeleteThreshold {
		return
	}
	res.LargeDeletion = true
	slog.Warn("large deletion executed — recoverable, but not forever",
		"removed_locally", res.DeletedLocal, "trashed_in_drive", res.DeletedRemote,
		"tracked_files", trackedFiles, "local_destination", deletionDestination(res.TrashAvailable))
}

// deletionDestination names where this machine's deletions went, for the log
// line and the activity entry.
func deletionDestination(binAvailable bool) string {
	if binAvailable {
		return "system bin"
	}
	return "quarantine"
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
