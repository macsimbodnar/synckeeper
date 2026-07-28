package tui

import (
	"strings"
	"testing"
	"time"
)

func liveModel(mut func(*Snapshot)) Model {
	s := testSnapshot(testRef())
	if mut != nil {
		mut(&s)
	}
	return New(Options{Snapshot: s, Width: 100, Height: 30, Clock: testRef})
}

// TestWithoutLiveDetailTheEstimateStaysMarked is the degradation contract: an
// old daemon, or none, leaves every panel on the database's figures — and the
// next-poll number keeps its "≈", because from the DB alone it *is* an estimate.
func TestWithoutLiveDetailTheEstimateStaysMarked(t *testing.T) {
	got := liveModel(nil).View() // Live zero value
	if !strings.Contains(got, "≈") {
		t.Error("without live detail the next-poll figure must stay marked as an estimate")
	}
	if strings.Contains(got, "syncing") {
		t.Error("without live detail the dashboard cannot claim a cycle is running")
	}
	// And the header must not invent a backend name it was never told.
	for _, invented := range []string{"FSEvents", "inotify", "polling only"} {
		if strings.Contains(got, invented) {
			t.Errorf("header names %q with no live detail", invented)
		}
	}
}

// TestLiveDeadlineDropsTheApproximation: once the daemon publishes its real
// poll deadline, the hedge is no longer honest — it is noise.
func TestLiveDeadlineDropsTheApproximation(t *testing.T) {
	m := liveModel(func(s *Snapshot) {
		s.Live = Live{
			Have:       true,
			Backend:    "fsevents",
			Poll:       45 * time.Second,
			NextTickAt: testRef().Add(125 * time.Second),
		}
	})
	got := m.View()
	if strings.Contains(got, "≈") {
		t.Error("with a real deadline the ≈ must go")
	}
	if !strings.Contains(got, "in 2m") {
		t.Errorf("the live deadline is not rendered:\n%s", firstLines(got, 8))
	}
	if !strings.Contains(got, "FSEvents") {
		t.Error("the header should name the live watch backend")
	}
}

// TestTickDueSaysSoRatherThanCountingBackwards: a Ticker whose receiver was busy
// fires the moment the loop returns, so a negative countdown would be a lie.
func TestTickDueSaysSoRatherThanCountingBackwards(t *testing.T) {
	m := liveModel(func(s *Snapshot) {
		s.Live = Live{Have: true, Backend: "fsnotify", NextTickAt: testRef().Add(-30 * time.Second), TickDue: true}
	})
	got := m.View()
	if !strings.Contains(got, "due now") {
		t.Errorf("an elapsed deadline should read 'due now':\n%s", firstLines(got, 8))
	}
	if strings.Contains(got, "-") && strings.Contains(got, "next poll   -") {
		t.Error("the countdown went negative")
	}
}

// TestWakePendingBeatsThePollDeadline: with a local change in the debounce
// window the poll figure is irrelevant — the next cycle is milliseconds away,
// and that is what the user wants to know.
func TestWakePendingBeatsThePollDeadline(t *testing.T) {
	m := liveModel(func(s *Snapshot) {
		s.Live = Live{
			Have:        true,
			Backend:     "fsevents",
			WakePending: true,
			WakeDueAt:   testRef().Add(400 * time.Millisecond),
			NextTickAt:  testRef().Add(40 * time.Second),
		}
	})
	got := m.View()
	if !strings.Contains(got, "local change — syncing") {
		t.Errorf("a pending wake is not reported:\n%s", firstLines(got, 8))
	}
	if strings.Contains(got, "in 40s") {
		t.Error("the poll deadline should give way to the imminent cycle")
	}
}

// TestRunningCycleIsTheTopLine: "is it doing something right now" is the
// question the DB could never answer between commits.
func TestRunningCycleIsTheTopLine(t *testing.T) {
	m := liveModel(func(s *Snapshot) {
		s.Live = Live{
			Have:           true,
			Backend:        "fsevents",
			CycleRunning:   true,
			CycleStartedAt: testRef().Add(-90 * time.Second),
			CycleElapsed:   90 * time.Second,
			CycleNumber:    412,
			NextTickAt:     testRef().Add(125 * time.Second),
		}
	})
	got := m.View()
	if !strings.Contains(got, "syncing") || !strings.Contains(got, "now") {
		t.Errorf("a running cycle is not reported:\n%s", firstLines(got, 8))
	}
	if !strings.Contains(got, "now · 1m") {
		t.Errorf("the elapsed time is missing:\n%s", firstLines(got, 8))
	}
	// It must come before the next-poll line: the running cycle is the answer.
	lines := strings.Split(got, "\n")
	syncingAt, pollAt := -1, -1
	for i, l := range lines {
		if syncingAt < 0 && strings.Contains(l, "syncing") {
			syncingAt = i
		}
		if pollAt < 0 && strings.Contains(l, "next poll") {
			pollAt = i
		}
	}
	if syncingAt < 0 || pollAt < 0 || syncingAt > pollAt {
		t.Errorf("the running cycle should sit above the next-poll line (syncing=%d poll=%d)", syncingAt, pollAt)
	}
}

// TestPollingOnlyIsNamedHonestly: the header must not say "watching" when the
// loop has fallen back to polling — the exact overstatement W3-adv-3 fixed for
// `status`, which the dashboard must not reintroduce.
func TestPollingOnlyIsNamedHonestly(t *testing.T) {
	m := liveModel(func(s *Snapshot) {
		s.Status.Daemon.Mode = "polling-only"
		s.Live = Live{Have: true, Backend: "polling", PollingOnly: true, NextTickAt: testRef().Add(125 * time.Second)}
	})
	got := m.View()
	if !strings.Contains(got, "polling only") {
		t.Errorf("a polling-only daemon is not named as such:\n%s", firstLines(got, 3))
	}
}

// TestLiveDetailNeverOverflowsWidth: the extra lines and the backend name must
// respect the width contract like everything else.
func TestLiveDetailNeverOverflowsWidth(t *testing.T) {
	for _, w := range []int{40, 60, 80, 100, 140} {
		s := testSnapshot(testRef())
		s.Live = Live{
			Have: true, Backend: "fsevents", CycleRunning: true,
			CycleElapsed: 90 * time.Second, WakePending: true,
			WakeDueAt: testRef().Add(time.Second), NextTickAt: testRef().Add(125 * time.Second),
		}
		m := New(Options{Snapshot: s, Width: w, Height: 24, Clock: testRef})
		for _, line := range strings.Split(m.View(), "\n") {
			if got := len([]rune(strings.TrimRight(line, " "))); got > w {
				t.Errorf("at width %d a line of %d runes: %q", w, got, line)
			}
		}
	}
}

func TestBackendLabel(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"fsevents", "FSEvents"},
		{"fsnotify", "inotify/kqueue"},
		{"polling", "polling only"},
		{"something-new", "something-new"},
	} {
		if got := (Live{Have: true, Backend: c.in}).backendLabel(); got != c.want {
			t.Errorf("backendLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := (Live{Backend: "fsevents"}).backendLabel(); got != "" {
		t.Errorf("with Have false the label must be empty, got %q", got)
	}
}
