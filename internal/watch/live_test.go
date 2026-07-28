package watch

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/control"
	"github.com/macsimbodnar/synckeeper/internal/engine"
)

// TestLiveStateTracksTheLoop covers the transitions the dashboard depends on,
// against a pinned clock — the state has no clock of its own, every method takes
// the instant, which is what makes this testable at all.
func TestLiveStateTracksTheLoop(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	l := newLiveState(45*time.Second, 500*time.Millisecond)

	// Before anything happens there is no deadline to report — better than
	// inventing one from process start.
	s := l.snapshot(now)
	if s.NextTickAt != 0 || s.TickDue {
		t.Errorf("a daemon that has not ticked yet reported a deadline: %+v", s)
	}
	if s.Backend != "polling" || !s.PollingOnly {
		t.Errorf("the initial backend should be the safe assumption, got %q", s.Backend)
	}

	l.setBackend("fsevents", false)
	l.noteTick(now)
	s = l.snapshot(now.Add(20 * time.Second))
	if s.Backend != "fsevents" || s.PollingOnly {
		t.Errorf("backend not reported: %+v", s)
	}
	if want := now.Add(45 * time.Second).Unix(); s.NextTickAt != want {
		t.Errorf("NextTickAt = %d, want %d (last tick + poll)", s.NextTickAt, want)
	}
	if s.TickDue {
		t.Error("the deadline is 25s away; TickDue must be false")
	}

	// A ticker whose receiver was busy fires as soon as the loop returns, so an
	// elapsed deadline is "due", never a negative countdown.
	if s := l.snapshot(now.Add(50 * time.Second)); !s.TickDue {
		t.Error("an elapsed deadline must report TickDue")
	}

	// A wake opens the debounce window and counts.
	l.noteWake(now.Add(30 * time.Second))
	s = l.snapshot(now.Add(30100 * time.Millisecond))
	if !s.WakePending {
		t.Error("a wake was not reported as pending")
	}
	if s.PendingChanges != 1 {
		t.Errorf("PendingChanges = %d after one wake, want 1", s.PendingChanges)
	}
	// Several events (one save can fire more than one) accumulate.
	l.noteWake(now.Add(30200 * time.Millisecond))
	l.noteWake(now.Add(30300 * time.Millisecond))
	if s := l.snapshot(now.Add(30400 * time.Millisecond)); s.PendingChanges != 3 {
		t.Errorf("PendingChanges = %d after three wakes, want 3", s.PendingChanges)
	}
	if want := now.Add(30*time.Second + 500*time.Millisecond).Unix(); s.WakeDueAt != want {
		t.Errorf("WakeDueAt = %d, want %d (wake + debounce)", s.WakeDueAt, want)
	}

	// The cycle consumes the wake and reports elapsed time while it runs.
	l.cycleBegin(now.Add(31*time.Second), 7)
	s = l.snapshot(now.Add(33 * time.Second))
	if s.WakePending {
		t.Error("the cycle should have consumed the pending wake")
	}
	if !s.CycleRunning || s.CycleNumber != 7 {
		t.Errorf("running cycle not reported: %+v", s)
	}
	// The count is "since the running cycle began", so the cycle resets it —
	// otherwise it would be a running total and mean nothing.
	if s.PendingChanges != 0 {
		t.Errorf("PendingChanges = %d after the cycle consumed the wake, want 0", s.PendingChanges)
	}
	// Changes arriving *during* a cycle are pending for the next one.
	l.noteWake(now.Add(32 * time.Second))
	if s := l.snapshot(now.Add(33 * time.Second)); s.PendingChanges != 1 || !s.WakePending {
		t.Errorf("a change during a cycle must be pending for the next: %+v", s)
	}
	if s.CycleElapsedMS != 2000 {
		t.Errorf("CycleElapsedMS = %d, want 2000", s.CycleElapsedMS)
	}

	l.cycleEnd()
	if s := l.snapshot(now.Add(34 * time.Second)); s.CycleRunning || s.CycleElapsedMS != 0 {
		t.Errorf("a finished cycle still reports as running: %+v", s)
	}

	// A reload of poll_interval_secs moves the published deadline too.
	l.setPoll(10 * time.Second)
	if s := l.snapshot(now.Add(34 * time.Second)); s.PollMS != 10000 {
		t.Errorf("PollMS = %d after a reload, want 10000", s.PollMS)
	}
}

// TestNoteBackendNeverOverstatesWatching: the reported mode must follow the loop
// into polling, which is the honesty bug W3-adv-3 fixed for `status` — the
// dashboard must not reintroduce it from a different direction.
func TestNoteBackendNeverOverstatesWatching(t *testing.T) {
	l := newLiveState(time.Second, time.Millisecond)
	fake := &countingBackend{}

	noteBackend(l, fake, false)
	if s := l.snapshot(time.Now()); s.Backend != "fake" || s.PollingOnly {
		t.Errorf("a live backend is not reported: %+v", s)
	}
	// Latched into polling with the backend still non-nil.
	noteBackend(l, fake, true)
	if s := l.snapshot(time.Now()); s.Backend != "polling" || !s.PollingOnly {
		t.Errorf("polling-only was not reported: %+v", s)
	}
	// And with no backend at all.
	noteBackend(l, nil, false)
	if s := l.snapshot(time.Now()); s.Backend != "polling" {
		t.Errorf("a nil backend must report polling, got %q", s.Backend)
	}
	noteBackend(nil, fake, false) // must not panic
}

// TestStatOverTheRealSocket is the end-to-end check: a running daemon answers
// `stat` with its live state, and the answer changes as the loop runs.
func TestStatOverTheRealSocket(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	sock := startWatcherWithControl(t, a, time.Hour, "")

	var s LiveSnapshot
	resp := call(t, sock, control.CmdStat, nil)
	if err := json.Unmarshal(resp.Data, &s); err != nil {
		t.Fatalf("stat payload: %v", err)
	}
	if s.Protocol != control.ProtocolVersion {
		t.Errorf("protocol = %d, want %d", s.Protocol, control.ProtocolVersion)
	}
	if s.PollMS != time.Hour.Milliseconds() {
		t.Errorf("PollMS = %d, want %d", s.PollMS, time.Hour.Milliseconds())
	}
	if s.Backend == "" {
		t.Error("stat reported no backend")
	}
	// The startup cycle has run by the time the socket answers a ping, so the
	// daemon is idle: a cycle must not be claimed as running.
	waitFor(t, "the startup cycle to finish", 5*time.Second, func() bool {
		resp := call(t, sock, control.CmdStat, nil)
		var s LiveSnapshot
		json.Unmarshal(resp.Data, &s)
		return !s.CycleRunning
	})

	// An unknown command still fails, so the additive `stat` did not turn the
	// dispatcher permissive.
	bad, err := control.Call(sock, control.Request{Cmd: "definitely-not-a-command"})
	if err != nil {
		t.Fatal(err)
	}
	if bad.OK {
		t.Error("an unknown command was accepted")
	}

	// A forced sync is visible while it runs — or has already finished, which is
	// equally fine; what matters is that stat keeps answering during a cycle.
	call(t, sock, control.CmdSync, nil)
	resp = call(t, sock, control.CmdStat, nil)
	if err := json.Unmarshal(resp.Data, &s); err != nil {
		t.Fatalf("stat during a sync: %v", err)
	}
}

// TestStatWithoutLiveStateDegrades: the handler must refuse rather than panic if
// it is ever wired without live state, since a client treats a refusal as
// "no live detail" and falls back to the database.
func TestStatWithoutLiveStateDegrades(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	w := &Watcher{Eng: a.eng, Poll: time.Hour, Debounce: time.Millisecond}
	h := w.controlHandler(make(chan engine.Options, 1), nil, newRecorder(a.eng.DB), nil)
	resp := h(context.Background(), control.Request{Cmd: control.CmdStat})
	if resp.OK {
		t.Error("stat answered OK with no live state")
	}
	if resp.Error == "" {
		t.Error("a refusal must say why")
	}
}
