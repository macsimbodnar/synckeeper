package tui

import "time"

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

	CycleRunning   bool
	CycleStartedAt time.Time
	CycleElapsed   time.Duration
	CycleNumber    int
}

// backendLabel names the watch mode for humans. Empty when unknown, so the
// header can simply omit it.
func (l Live) backendLabel() string {
	if !l.Have {
		return ""
	}
	switch l.Backend {
	case "fsevents":
		return "FSEvents"
	case "fsnotify":
		return "inotify/kqueue"
	case "polling":
		return "polling only"
	default:
		return l.Backend
	}
}
