package watch

import (
	"sync"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/engine"
)

// liveState is what the daemon knows and the state DB does not: whether a cycle
// is running *right now*, which watch backend is live, when the poll timer is
// actually due, and whether a local change is sitting in the debounce window.
//
// It exists because the DB is written once per cycle (spec §8.2), which is the
// wrong granularity for a dashboard: between two cycles the recorded next-poll
// figure is an estimate that any local change pre-empts. This is published
// read-only over the control socket (`stat`, W15-U5) and is never load-bearing
// for syncing — nothing in the engine reads it.
type liveState struct {
	mu sync.Mutex

	poll     time.Duration
	debounce time.Duration

	backend     string // "fsevents" | "fsnotify" | "polling"
	pollingOnly bool

	lastTickAt  time.Time // last time the poll ticker actually fired
	wakeAt      time.Time // last watcher wake-up (the debounce window opened)
	wakePending bool      // a wake is armed and no cycle has consumed it yet
	// pendingChanges counts wake-ups since the cycle that will consume them. A
	// count, not the paths: the paths would mean changing the fsWatcher
	// interface and both backends, and this answers the question a user
	// actually asks ("is something about to sync, and roughly how much?") for
	// no per-event work at all — the increment rides inside the mutex noteWake
	// already takes (Max, 2026-07-28, decisions.md).
	pendingChanges int

	cycleRunning   bool
	cycleStartedAt time.Time
	cycleNumber    int

	// stage is the running cycle's own reporter, owned by the engine package.
	// Read straight through: it has its own lock, and copying the string here
	// per cycle would only add a way for the two to disagree.
	stage *engine.StageReporter
}

// LiveSnapshot is the wire shape of `stat`. Exported because the client decodes
// it: the daemon owns the contract, so there is exactly one definition of it. Times are unix seconds (0 = unset)
// and durations milliseconds, matching the rest of the protocol's plain-JSON
// style rather than introducing time formats a future UI would have to parse.
type LiveSnapshot struct {
	Protocol int `json:"protocol"`

	Backend     string `json:"backend"`
	PollingOnly bool   `json:"polling_only"`

	PollMS     int64 `json:"poll_ms"`
	DebounceMS int64 `json:"debounce_ms"`

	// NextTickAt is the poll timer's real deadline, not the estimate the DB
	// carries. TickDue is true when it has already elapsed — a Ticker whose
	// receiver was busy fires as soon as the loop comes back round.
	NextTickAt int64 `json:"next_tick_at"`
	TickDue    bool  `json:"tick_due"`

	// WakePending means a local change is inside the debounce window: the next
	// cycle is milliseconds away, not a poll interval away. PendingChanges is
	// how many wake-ups have arrived since the last cycle began — the honest
	// bound, since one save can fire several events and the scanner may find
	// nothing to do for some of them.
	WakePending    bool  `json:"wake_pending"`
	WakeDueAt      int64 `json:"wake_due_at"`
	PendingChanges int   `json:"pending_changes"`

	CycleRunning   bool  `json:"cycle_running"`
	CycleStartedAt int64 `json:"cycle_started_at"`
	CycleElapsedMS int64 `json:"cycle_elapsed_ms"`
	CycleNumber    int   `json:"cycle_number"`

	// Stage is which part of the cycle is running ("checking Drive",
	// "scanning files", …) and StageActions the size of the plan being
	// executed once that is known — the total is free from the plan; there is
	// deliberately no done-of-total counter (decisions.md 2026-07-28).
	Stage        string `json:"stage"`
	StageActions int    `json:"stage_actions"`
}

func newLiveState(poll, debounce time.Duration) *liveState {
	return &liveState{
		poll: poll, debounce: debounce, backend: "polling", pollingOnly: true,
		stage: engine.NewStageReporter(),
	}
}

// stageReporter is handed to every cycle through engine.Options.
func (l *liveState) stageReporter() *engine.StageReporter {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stage
}

func (l *liveState) setBackend(name string, pollingOnly bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.backend, l.pollingOnly = name, pollingOnly
}

func (l *liveState) setPoll(d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.poll = d
}

// noteTick records that the poll ticker fired. A Ticker gives no access to its
// schedule, so the deadline is derived from the last fire.
func (l *liveState) noteTick(at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lastTickAt = at
}

// noteWake records a watcher wake-up: the debounce window is open and a cycle
// is imminent.
func (l *liveState) noteWake(at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.wakeAt, l.wakePending = at, true
	l.pendingChanges++
}

func (l *liveState) cycleBegin(at time.Time, number int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cycleRunning, l.cycleStartedAt, l.cycleNumber = true, at, number
	// This cycle is the wake being consumed, so the count starts over: it means
	// "changes seen since the running cycle began", which is what makes it a
	// pending count rather than a running total.
	l.wakePending, l.pendingChanges = false, 0
}

func (l *liveState) cycleEnd() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cycleRunning = false
}

// snapshot renders the current state as of now.
func (l *liveState) snapshot(now time.Time) LiveSnapshot {
	l.mu.Lock()
	defer l.mu.Unlock()

	s := LiveSnapshot{
		Backend:        l.backend,
		PollingOnly:    l.pollingOnly,
		PollMS:         l.poll.Milliseconds(),
		DebounceMS:     l.debounce.Milliseconds(),
		WakePending:    l.wakePending,
		PendingChanges: l.pendingChanges,
		CycleRunning:   l.cycleRunning,
		CycleNumber:    l.cycleNumber,
	}
	if !l.lastTickAt.IsZero() && l.poll > 0 {
		next := l.lastTickAt.Add(l.poll)
		s.NextTickAt = next.Unix()
		s.TickDue = !next.After(now)
	}
	if l.wakePending && !l.wakeAt.IsZero() {
		s.WakeDueAt = l.wakeAt.Add(l.debounce).Unix()
	}
	if l.cycleRunning && !l.cycleStartedAt.IsZero() {
		s.CycleStartedAt = l.cycleStartedAt.Unix()
		s.CycleElapsedMS = now.Sub(l.cycleStartedAt).Milliseconds()
		s.Stage, s.StageActions = l.stage.Read()
	}
	return s
}
