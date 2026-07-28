package engine

import "sync"

// Sync's stages, in the order a cycle runs them. The wording is what a user
// reads on the dashboard, so it names the *thing being waited on* rather than
// the function doing the waiting: a cycle stuck on a flaky network reads
// "checking Drive", which points at the network instead of at us.
const (
	StageStarting     = "starting"
	StageCheckingDriv = "checking Drive"
	StageScanning     = "scanning files"
	StagePlanning     = "planning"
	StageTransferring = "transferring"
	StageFinishing    = "finishing"
)

// StageReporter carries which stage a running cycle is in, for reporting only.
//
// It is a type the **engine owns**, deliberately, rather than a caller-supplied
// callback: with a callback, foreign code would run inside the sync path, and a
// reporting bug that panicked or blocked could stop the thing it was meant to
// observe. Here the engine only ever calls a method on its own type, so that
// entire class of failure does not exist. The reader is on another goroutine
// (the control socket answering `stat`), hence the mutex.
//
// A nil *StageReporter is valid and does nothing, so `Options.Stage` is
// optional and a one-shot `sync` pays nothing for it.
type StageReporter struct {
	mu      sync.Mutex
	stage   string
	actions int // actions in the plan being executed; 0 until it is known
}

// NewStageReporter returns a reporter in its pre-cycle state.
func NewStageReporter() *StageReporter { return &StageReporter{} }

func (r *StageReporter) set(stage string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.stage = stage
	r.mu.Unlock()
}

// setActions records the size of the plan about to be executed, which is known
// from the plan itself — no counting inside the executor (W15-U5: the done-of-
// total counter was deliberately left out rather than instrument the write path
// for a display; decisions.md).
func (r *StageReporter) setActions(n int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.actions = n
	r.mu.Unlock()
}

// Read reports the current stage and planned action count.
func (r *StageReporter) Read() (stage string, actions int) {
	if r == nil {
		return "", 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stage, r.actions
}

// Reset clears the reporter between cycles so a finished cycle's last stage is
// never mistaken for a running one.
func (r *StageReporter) Reset() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.stage, r.actions = "", 0
	r.mu.Unlock()
}

// SetForTest sets the stage from outside the package. It exists for tests in
// other packages (the daemon's `stat` plumbing) and is never called in
// production code, where only Sync writes the reporter.
func (r *StageReporter) SetForTest(stage string, actions int) {
	r.set(stage)
	r.setActions(actions)
}
