package tui

import (
	"time"

	"github.com/macsimbodnar/synckeeper/internal/status"
)

// Live is the in-memory detail only the running daemon knows (W15-U5, the
// `stat` control command). It is optional by construction: a daemon too old to
// answer, or one that is not running at all, leaves Have false and every panel
// falls back to what the state DB carries.
type Live struct {
	Have bool // a daemon answered `stat`

	Backend     string // "fsevents" | "fsnotify" | "polling"
	PollingOnly bool

	Poll     time.Duration
	Debounce time.Duration

	// NextTickAt is the poll timer's real deadline — the reason the dashboard
	// can drop the "≈" it has to show when working from the DB alone. TickDue
	// means it has already elapsed and the loop will pick it up immediately.
	NextTickAt time.Time
	TickDue    bool

	// WakePending: a local change is inside the debounce window, so the next
	// cycle is milliseconds away rather than a poll interval away.
	WakePending bool
	WakeDueAt   time.Time
	// PendingChanges is how many watcher wake-ups have arrived since the last
	// cycle began — a bound on what is about to sync, not a file list.
	PendingChanges int

	// EventOverflows is how many times the kernel's event queue overflowed
	// since the daemon started, dropping events (S001-L4b). In-memory in the
	// daemon: it starts again at zero on restart, and the state DB never
	// carries it, so it is shown only while a daemon answers `stat`.
	EventOverflows int

	// BackingOff: the last cycle failed and the loop is waiting before it
	// retries, until RetryAt. It outranks the poll deadline above, which is
	// derived from the last tick and stops meaning anything during the wait —
	// the panel used to read it anyway and promise a poll that never came
	// (W21).
	BackingOff bool
	RetryAt    time.Time

	CycleRunning   bool
	CycleStartedAt time.Time
	CycleElapsed   time.Duration
	CycleNumber    int

	// Stage is which part of the running cycle is executing, and StageActions
	// the size of the plan once it is known.
	Stage        string
	StageActions int

	// Standstill is the daemon's report that its plan has been refused cycle
	// after cycle (S002). Carried here only to reach status.Gather, which owns
	// it: the attention block reads the status snapshot, like `status --plain`.
	Standstill *status.Standstill
}

// backendLabel names the watch backend for humans. Empty when there is none to
// name — the daemon has not answered, or it is polling — so the header can
// simply omit it. The header appends this to the daemon's own mode word, and
// the mode already says "polling-only"; naming it twice produced the reading
// `polling-only (polling only)`, in exactly the degraded state a user most
// needs to read cleanly (W15-U6).
func (l Live) backendLabel() string {
	if !l.Have || l.PollingOnly {
		return ""
	}
	switch l.Backend {
	case "fsevents":
		return "FSEvents"
	case "fsnotify":
		return "inotify/kqueue"
	case "polling":
		return ""
	default:
		return l.Backend
	}
}
