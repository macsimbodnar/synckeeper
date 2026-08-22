package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/macsimbodnar/synckeeper/internal/status"
)

// backoffModel is the field state W21 fixes: the last cycle failed, the loop is
// parked in waitBackoff, and the poll deadline it published has already
// elapsed — the ticker keeps firing into a buffer nobody is receiving from.
func backoffModel(ref time.Time) Model {
	s := testSnapshot(ref)
	s.Status.Daemon.Mode = status.ModeBackoff
	s.Status.Daemon.LastError = "refresh remote state: oauth2: \"invalid_grant\""
	s.Status.Daemon.NextPollAt = ref.Add(11*time.Minute + 40*time.Second).Unix()
	s.Live = Live{
		Have: true, Backend: "fsnotify",
		Poll:       45 * time.Second,
		NextTickAt: ref.Add(-31 * time.Second),
		TickDue:    true,
		BackingOff: true,
		RetryAt:    ref.Add(11*time.Minute + 40*time.Second),
	}
	return New(Options{Snapshot: s, Width: 100, Height: 30, Clock: testRef})
}

// TestTheDashboardNeverPromisesAPollWhileBackingOff is the bug itself: an
// elapsed TickDue used to render `next poll: due now` for the whole of a
// 12-minute wait, which is the "I pressed sync and nothing happened" experience
// drawn on the screen.
func TestTheDashboardNeverPromisesAPollWhileBackingOff(t *testing.T) {
	frame := backoffModel(testRef()).View()

	if strings.Contains(frame, "due now") {
		t.Errorf("the frame promises a poll while the daemon is backing off:\n%s", frame)
	}
	if strings.Contains(frame, "next poll") {
		t.Errorf("the frame still names a poll deadline that means nothing during a backoff:\n%s", frame)
	}
	if !strings.Contains(frame, "retry") || !strings.Contains(frame, "in 11m") {
		t.Errorf("the frame does not say when the retry is due:\n%s", frame)
	}
	// Exact, not "≈": this figure is the daemon's own deadline.
	if strings.Contains(frame, "≈ in 11m") {
		t.Errorf("a live deadline was marked as an estimate:\n%s", frame)
	}
}

// TestTheAttentionBlockNamesTheRetry: the error line says what broke; without
// this one nothing on the screen said the daemon had not given up, or how to
// stop waiting for it. The hint is only truthful because of W20.
func TestTheAttentionBlockNamesTheRetry(t *testing.T) {
	frame := backoffModel(testRef()).View()
	if !strings.Contains(frame, "⚠ retrying after a failure — next try in 11m; press `s` to retry now") {
		t.Errorf("the attention block does not name the pending retry:\n%s", frame)
	}
}

// TestTheBackoffModeWordIsNotJustAnotherGreySegment: the header's mode word was
// the only honest indicator on the screen, painted the same grey as `pid 4242`.
func TestTheBackoffModeWordIsNotJustAnotherGreySegment(t *testing.T) {
	m := backoffModel(testRef())
	m.color = true
	header := strings.SplitN(m.View(), "\n", 2)[0]

	warn := lipgloss.NewStyle().Foreground(colWarn).Render("backoff")
	if !strings.Contains(header, warn) {
		t.Errorf("the backoff mode word is not painted warn:\n%q", header)
	}
}

// TestTheBackoffDeadlineFallsBackToTheDatabase covers the daemon that cannot
// publish the deadline — too old to answer `stat`, or a socket that did not
// answer. The DB's figure is the same estimate every other row there carries,
// so it is marked, matching the live-vs-DB convention the rest of the panel
// already follows.
func TestTheBackoffDeadlineFallsBackToTheDatabase(t *testing.T) {
	ref := testRef()
	s := testSnapshot(ref)
	s.Status.Daemon.Mode = status.ModeBackoff
	s.Status.Daemon.NextPollAt = ref.Add(11*time.Minute + 40*time.Second).Unix()
	s.Live = Live{} // no daemon answered `stat`
	frame := New(Options{Snapshot: s, Width: 100, Height: 30, Clock: testRef}).View()

	if !strings.Contains(frame, "retry") {
		t.Errorf("the DB-only fallback does not name the retry:\n%s", frame)
	}
	if !strings.Contains(frame, "≈ in 11m") {
		t.Errorf("the DB-only deadline is not shown as an estimate:\n%s", frame)
	}
	if strings.Contains(frame, "next poll") {
		t.Errorf("the DB fallback still labels the wait a poll:\n%s", frame)
	}
}

// TestAPausedDaemonIsNotRetrying: pausing stops the cycles that would clear the
// mode, so the DB row goes stale as `backoff` and stays there — no retry is
// coming until the user resumes, and the frame must not promise one.
func TestAPausedDaemonIsNotRetrying(t *testing.T) {
	ref := testRef()
	s := testSnapshot(ref)
	s.Status.Daemon.Paused = true
	s.Status.Daemon.Mode = status.ModeBackoff
	s.Status.Daemon.NextPollAt = ref.Add(11*time.Minute + 40*time.Second).Unix()
	frame := New(Options{Snapshot: s, Width: 100, Height: 30, Clock: testRef}).View()

	if strings.Contains(frame, "retry") {
		t.Errorf("a paused daemon was reported as retrying:\n%s", frame)
	}
}
