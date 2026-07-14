package watch

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/engine"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// HeartbeatInterval is how often the daemon refreshes its status row while
// otherwise idle, so a quiet daemon still looks alive. Read-only commands
// treat a heartbeat older than a few multiples of this as "likely dead".
const HeartbeatInterval = 10 * time.Second

// Daemon modes reported in status.
const (
	ModeWatching    = "watching"     // fsnotify active
	ModePollingOnly = "polling-only" // watch failed; polling covers the tree
	ModeBackoff     = "backoff"      // last cycle errored; retrying with backoff
	ModePaused      = "paused"       // auto-sync suspended via `pause`
	ModeStopped     = "stopped"      // clean shutdown
)

// recorder maintains the daemon_status row and the activity ring. Its fields
// are guarded because the heartbeat ticker persists concurrently with the
// main sync loop's updates.
type recorder struct {
	db *statedb.DB

	mu sync.Mutex
	s  statedb.DaemonStatus
}

func newRecorder(db *statedb.DB) *recorder {
	r := &recorder{db: db}
	r.s = statedb.DaemonStatus{
		Running:   true,
		PID:       os.Getpid(),
		StartedAt: time.Now().Unix(),
		Mode:      ModeWatching,
	}
	r.persist()
	return r
}

// heartbeat persists the current status on a ticker until ctx is cancelled,
// so the daemon stays visibly alive between (and during long) sync cycles.
func (r *recorder) heartbeat(ctx context.Context) {
	t := time.NewTicker(HeartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.persist()
		}
	}
}

func (r *recorder) persist() {
	r.mu.Lock()
	r.s.LastHeartbeatAt = time.Now().Unix()
	snapshot := r.s
	r.mu.Unlock()
	if err := r.db.SetDaemonStatus(snapshot); err != nil {
		slog.Debug("persist daemon status", "err", err)
	}
}

func (r *recorder) setMode(mode string) {
	r.mu.Lock()
	r.s.Mode = mode
	r.mu.Unlock()
	r.persist()
}

// setPaused records the pause state; while paused the mode reads "paused" so
// status makes the suspended auto-sync obvious.
func (r *recorder) setPaused(p bool) {
	r.mu.Lock()
	r.s.Paused = p
	if p {
		r.s.Mode = ModePaused
	}
	r.mu.Unlock()
	r.persist()
}

func (r *recorder) isPaused() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.s.Paused
}

// cycleDone records the outcome of one sync cycle and its next-poll estimate.
func (r *recorder) cycleDone(res *engine.Result, dur time.Duration, syncErr error, mode string, nextPoll time.Time, guardBlocked bool, guardReason string) {
	r.mu.Lock()
	now := time.Now().Unix()
	r.s.Mode = mode
	r.s.NextPollAt = nextPoll.Unix()
	r.s.GuardBlocked = guardBlocked
	r.s.GuardReason = guardReason
	if syncErr == nil {
		r.s.LastSyncAt = now
		r.s.LastError = ""
	} else {
		r.s.LastError = syncErr.Error()
	}
	if res != nil {
		cs := statedb.CycleSummary{
			Actions: len(res.Plan), Executed: res.Executed, Failed: res.Failed,
			DurationMS: dur.Milliseconds(),
		}
		if b, err := json.Marshal(cs); err == nil {
			r.s.LastCycleJSON = string(b)
		}
	}
	r.mu.Unlock()
	r.persist()
}

// stop marks a clean shutdown so status reports "stopped" rather than
// waiting for the heartbeat to go stale.
func (r *recorder) stop() {
	r.mu.Lock()
	r.s.Running = false
	r.s.Mode = ModeStopped
	r.mu.Unlock()
	r.persist()
}

// recordActivity appends the interesting actions of a completed cycle to the
// ring. When any action failed we can't tell which succeeded, so we record a
// summary plus the error strings instead of claiming per-action success.
func (r *recorder) recordActivity(res *engine.Result) {
	if res == nil {
		return
	}
	now := time.Now().Unix()
	if res.Failed == 0 {
		for _, a := range res.Plan {
			kind := activityKind(a.Type)
			if kind == "" {
				continue
			}
			detail := ""
			if a.NewRelPath != "" {
				detail = "-> " + a.NewRelPath
			}
			r.append(statedb.Activity{TS: now, Kind: kind, RelPath: a.RelPath, Detail: detail})
		}
		return
	}
	r.append(statedb.Activity{TS: now, Kind: "error", Detail: cycleSummaryText(res)})
	for _, e := range res.Errors {
		r.append(statedb.Activity{TS: now, Kind: "error", Detail: e})
	}
}

// recordError logs a failed cycle as a single activity entry. On failure we
// don't know which planned actions ran, so we never claim per-action success.
func (r *recorder) recordError(err error) {
	r.append(statedb.Activity{TS: time.Now().Unix(), Kind: "error", Detail: err.Error()})
}

func (r *recorder) append(a statedb.Activity) {
	if err := r.db.AppendActivity(a); err != nil {
		slog.Debug("append activity", "err", err)
	}
}

func cycleSummaryText(res *engine.Result) string {
	cs := statedb.CycleSummary{Actions: len(res.Plan), Executed: res.Executed, Failed: res.Failed}
	b, _ := json.Marshal(cs)
	return string(b)
}

// activityKind maps a plan action to a short verb, or "" to skip actions that
// aren't interesting to a human watching activity (metadata-only refreshes).
func activityKind(t reconcile.Type) string {
	switch t {
	case reconcile.Upload:
		return "upload"
	case reconcile.UpdateRemote:
		return "update"
	case reconcile.Download:
		return "download"
	case reconcile.TrashRemote:
		return "trash"
	case reconcile.QuarantineLocal:
		return "quarantine"
	case reconcile.MoveLocal, reconcile.MoveRemote:
		return "move"
	case reconcile.MkdirLocal, reconcile.MkdirRemote:
		return "mkdir"
	case reconcile.ConflictBackup:
		return "conflict"
	default:
		return "" // Record, Forget: no user-visible change
	}
}
