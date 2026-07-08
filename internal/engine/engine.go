// Package engine orchestrates one sync cycle: guards, local scan, remote
// delta, reconcile, execute. It is shared by `sync` now and `watch` in
// phase 3, and is driven against the fake Drive in scenario tests.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/config"
	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/executor"
	"github.com/macsimbodnar/synckeeper/internal/guards"
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
}

// Options tweak one run.
type Options struct {
	DryRun         bool
	ConfirmDeletes bool
}

// Result is what one run did (or, dry-run, would do).
type Result struct {
	Plan     []reconcile.Action
	Skips    []reconcile.Skip
	Executed int
	Failed   int
	Errors   []string
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
	remote, remoteSkips, err := remotedelta.Snapshot(e.DB, e.RootID, e.Cfg.Engine.Ignore)
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

	if err := guards.CheckMassDelete(plan, len(baseItems), e.Cfg.Engine.MassDeleteThreshold, opts.ConfirmDeletes); err != nil {
		return res, err
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
	return res, nil
}

// nowFunc is swappable in tests that need deterministic conflict names.
var nowFunc = defaultNow

func defaultNow() (t time.Time) { return time.Now() }
