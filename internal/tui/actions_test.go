package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// fakeActions records what the dashboard asked for, and can be made slow or
// failing. It stands in for the control socket exactly as W5.1's injected
// service installer stands in for launchd.
type fakeActions struct {
	calls   []string
	err     error
	release chan struct{} // when non-nil, the call blocks until closed
}

func (f *fakeActions) hook(name string) func() error {
	return func() error {
		f.calls = append(f.calls, name)
		if f.release != nil {
			<-f.release
		}
		return f.err
	}
}

func (f *fakeActions) wire() *Actions {
	return &Actions{
		SyncNow: f.hook("sync"),
		Pause:   f.hook("pause"),
		Resume:  f.hook("resume"),
		Reload:  f.hook("reload"),
	}
}

func actionModel(f *fakeActions) Model {
	var a *Actions
	if f != nil {
		a = f.wire()
	}
	return New(Options{
		Snapshot: testSnapshot(testRef()),
		Width:    100, Height: 30,
		Clock:   testRef,
		Actions: a,
		Refresh: func() Snapshot { return testSnapshot(testRef()) },
	})
}

// TestEachActionKeyCallsItsControlCommand: the four keys map to the four calls
// the CLI already makes, and each runs off the event loop (the key handler
// returns a command rather than blocking).
func TestEachActionKeyCallsItsControlCommand(t *testing.T) {
	cases := []struct{ key, want string }{
		{"s", "sync"},
		{"p", "pause"},
		{"P", "resume"},
		{"R", "reload"},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			f := &fakeActions{}
			m := actionModel(f)

			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(c.key)})
			if cmd == nil {
				t.Fatalf("key %q produced no command — the call would run on the event loop", c.key)
			}
			if len(f.calls) != 0 {
				t.Errorf("key %q called the daemon synchronously; a 30s control timeout would freeze the UI", c.key)
			}

			// The in-flight state is visible while the call runs.
			nm := next.(Model)
			if nm.pending == "" {
				t.Errorf("key %q did not mark an action in flight", c.key)
			}
			if !strings.Contains(nm.View(), "asking the daemon to") {
				t.Errorf("key %q gave no in-flight feedback", c.key)
			}

			// Running the command performs the call and reports the outcome.
			msg := cmd()
			done, ok := msg.(actionDoneMsg)
			if !ok {
				t.Fatalf("key %q returned %T, want actionDoneMsg", c.key, msg)
			}
			if len(f.calls) != 1 || f.calls[0] != c.want {
				t.Errorf("key %q called %v, want [%s]", c.key, f.calls, c.want)
			}
			final, _ := nm.Update(done)
			fm := final.(Model)
			if fm.pending != "" {
				t.Error("the in-flight marker outlived the action")
			}
			if got, _ := fm.visibleNotice(); got.level != noticeGood {
				t.Errorf("a successful %s was not reported as success: %+v", c.want, got)
			}
		})
	}
}

// TestSyncNowNeverConfirmsDeletes is the safety property of U4: the daemon never
// self-confirms a mass deletion (spec §8.1), so a keystroke must not either. The
// notice reports what happened, and the guard's own hint — the command line —
// stays the only way out.
func TestSyncNowNeverConfirmsDeletes(t *testing.T) {
	f := &fakeActions{}
	snap := testSnapshot(testRef())
	snap.Status.Daemon.GuardBlocked = true
	snap.Status.Daemon.GuardReason = "plan deletes 1117 of 1118 tracked files"

	m := New(Options{
		Snapshot: snap, Width: 100, Height: 40, Clock: testRef,
		Actions: f.wire(),
		Refresh: func() Snapshot { return snap },
	})

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	done := cmd().(actionDoneMsg)
	final, _ := next.(Model).Update(done)
	view := final.(Model).View()

	if len(f.calls) != 1 || f.calls[0] != "sync" {
		t.Fatalf("calls = %v", f.calls)
	}
	// The dashboard has no way to pass --confirm-deletes: the Actions surface
	// carries no argument at all. Assert the user is still pointed at the CLI.
	if !strings.Contains(view, "--confirm-deletes") {
		t.Error("with a guard block showing, the frame must still name the only way to release it")
	}
	if strings.Contains(strings.ToLower(final.(Model).notice.text), "confirm") {
		t.Errorf("the sync notice implies deletions were confirmed: %q", final.(Model).notice.text)
	}
}

// TestActionFailureIsReportedNotSwallowed: a control error must reach the user
// verbatim, since it is usually the actionable part ("not running", a protocol
// mismatch, a busy lock).
func TestActionFailureIsReportedNotSwallowed(t *testing.T) {
	f := &fakeActions{err: errors.New("the watch daemon is not running")}
	m := actionModel(f)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	final, _ := next.(Model).Update(cmd().(actionDoneMsg))
	fm := final.(Model)

	got, ok := fm.visibleNotice()
	if !ok || got.level != noticeBad {
		t.Fatalf("a failed action was not reported as a failure: %+v", got)
	}
	if !strings.Contains(got.text, "the watch daemon is not running") {
		t.Errorf("the error text was lost: %q", got.text)
	}
	if !strings.Contains(fm.View(), "the watch daemon is not running") {
		t.Error("the failure is not visible in the frame")
	}
}

// TestActionsWithoutADaemonSaySo: with no Actions wired at all, the keys must
// explain rather than do nothing.
func TestActionsWithoutADaemonSaySo(t *testing.T) {
	m := actionModel(nil)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if cmd != nil {
		t.Error("there is nothing to call, so there should be no command")
	}
	nm := next.(Model)
	got, ok := nm.visibleNotice()
	if !ok || got.level != noticeBad {
		t.Fatalf("no notice for an unavailable action: %+v", got)
	}
	for _, want := range []string{"no daemon", "watch", "service install"} {
		if !strings.Contains(got.text, want) {
			t.Errorf("notice %q does not mention %q", got.text, want)
		}
	}
}

// TestOnlyOneActionInFlight: the control protocol is one request per connection,
// and a second sync-now mid-flight would only confuse the report.
func TestOnlyOneActionInFlight(t *testing.T) {
	f := &fakeActions{release: make(chan struct{})}
	m := actionModel(f)

	first, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	fm := first.(Model)

	second, cmd2 := fm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	if cmd2 != nil {
		t.Error("a second action started while the first was in flight")
	}
	sm := second.(Model)
	if got, _ := sm.visibleNotice(); !strings.Contains(got.text, "already asking") {
		t.Errorf("the busy notice is missing: %q", got.text)
	}
	if sm.pending != verbSync {
		t.Errorf("pending = %q, want %q", sm.pending, verbSync)
	}

	// Let the first finish; the dashboard accepts actions again.
	close(f.release)
	done := cmd().(actionDoneMsg)
	final, _ := sm.Update(done)
	_, cmd3 := final.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	if cmd3 == nil {
		t.Fatal("after the first action completed, a new one should be accepted")
	}
	cmd3() // running it is what performs the call
	if len(f.calls) < 2 || f.calls[0] != "sync" || f.calls[1] != "pause" {
		t.Errorf("calls = %v, want sync then pause", f.calls)
	}
}

// TestActionRefreshesImmediately: after pausing, the panels must not wait out
// the interval to show it.
func TestActionRefreshesImmediately(t *testing.T) {
	f := &fakeActions{}
	refreshes := 0
	m := New(Options{
		Snapshot: testSnapshot(testRef()),
		Width:    100, Height: 30, Clock: testRef,
		Actions: f.wire(),
		Refresh: func() Snapshot { refreshes++; return testSnapshot(testRef()) },
	})

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	if refreshes != 0 {
		t.Error("pressing the key should not refresh; the result should")
	}
	if _, _ = next.(Model).Update(cmd().(actionDoneMsg)); refreshes != 1 {
		t.Errorf("the action result triggered %d refreshes, want 1", refreshes)
	}
}

// TestNoticeExpires: a result line is a report, not state — it must not linger
// long enough to be mistaken for the current situation.
func TestNoticeExpires(t *testing.T) {
	f := &fakeActions{}
	now := testRef()
	m := New(Options{
		Snapshot: testSnapshot(testRef()),
		Width:    100, Height: 30,
		Clock:   func() time.Time { return now },
		Actions: f.wire(),
		Refresh: func() Snapshot { return testSnapshot(testRef()) },
	})

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	final, _ := next.(Model).Update(cmd().(actionDoneMsg))
	fm := final.(Model)

	if _, ok := fm.visibleNotice(); !ok {
		t.Fatal("the notice is not visible immediately after the action")
	}
	now = now.Add(noticeTTL + time.Second)
	if _, ok := fm.visibleNotice(); ok {
		t.Error("the notice outlived its TTL")
	}
	if strings.Contains(fm.View(), "paused —") {
		t.Error("an expired notice is still drawn")
	}

	// An in-flight action's notice never expires: it describes now, not the past.
	slow := &fakeActions{release: make(chan struct{})}
	sm := actionModel(slow)
	pending, _ := sm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	pm := pending.(Model)
	pm.clock = func() time.Time { return testRef().Add(time.Hour) }
	if _, ok := pm.visibleNotice(); !ok {
		t.Error("an in-flight notice expired while the action was still running")
	}
	close(slow.release)
}

// TestActionKeysAreListedWhereTheyApply: the footer offers them on the overview,
// the help lists all four with the confirm-deletes caveat, and an in-flight
// action replaces the hint so the user knows something is happening.
func TestActionKeysAreListedWhereTheyApply(t *testing.T) {
	f := &fakeActions{release: make(chan struct{})}
	m := actionModel(f)

	if got := m.View(); !strings.Contains(got, "s sync") || !strings.Contains(got, "p/P pause") {
		t.Error("the overview footer does not offer the actions")
	}

	help, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	hv := help.(Model).View()
	for _, want := range []string{"sync now", "pause / resume", "reload config.toml", "--confirm-deletes"} {
		if !strings.Contains(hv, want) {
			t.Errorf("help does not document %q", want)
		}
	}

	inflight, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if got := inflight.(Model).View(); !strings.Contains(got, "… "+verbSync) {
		t.Error("the footer does not show the action in flight")
	}
	close(f.release)
}

// TestActionNoticeNeverOverflowsWidth: the notice carries a daemon error string,
// which can be very long.
func TestActionNoticeNeverOverflowsWidth(t *testing.T) {
	long := strings.Repeat("refresh remote state: dial tcp: lookup www.googleapis.com: no such host; ", 5)
	f := &fakeActions{err: errors.New(long)}
	for _, w := range []int{40, 60, 80, 100} {
		m := New(Options{
			Snapshot: testSnapshot(testRef()),
			Width:    w, Height: 20, Clock: testRef,
			Actions: f.wire(),
			Refresh: func() Snapshot { return testSnapshot(testRef()) },
		})
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
		final, _ := next.(Model).Update(cmd().(actionDoneMsg))
		for _, line := range strings.Split(final.(Model).View(), "\n") {
			if got := len([]rune(strings.TrimRight(line, " "))); got > w {
				t.Errorf("at width %d a line of %d runes: %q", w, got, line)
			}
		}
	}
}
