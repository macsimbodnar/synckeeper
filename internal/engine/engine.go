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

	caseFold *bool // lazily probed once; sync cycles are serialized
	normFold *bool
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

	plan, planSkips := reconcile.Plan(reconcile.Input{
		Base:    base,
		Local:   local,
		Remote:  remote,
		Machine: e.Cfg.Engine.MachineName,
		Now:     nowFunc(),
	})
	res := &Result{Plan: plan}
	res.Skips = append(res.Skips, remoteSkips...)
	res.Skips = append(res.Skips, localSkips...)
	res.Skips = append(res.Skips, planSkips...)

	if guardErr := guards.CheckMassDelete(plan, len(baseItems), e.Cfg.Engine.MassDeleteThreshold, opts.ConfirmDeletes); guardErr != nil {
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
