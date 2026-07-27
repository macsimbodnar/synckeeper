package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Actions are the daemon commands the dashboard can ask for. Each is the
// control-socket call the equivalent CLI command already makes (W15-U4 adds no
// daemon capability), and each may block for as long as the control timeout, so
// they run off the event loop.
//
// A nil field means the dashboard offers that action but reports it
// unavailable; a nil Actions means the caller wired none at all.
type Actions struct {
	SyncNow func() error
	Pause   func() error
	Resume  func() error
	Reload  func() error
}

// actionDoneMsg carries the outcome back to the event loop.
type actionDoneMsg struct {
	verb string // what was asked, in the past tense used in the notice
	err  error
}

// noticeTTL is how long a result line stays before it clears itself. Long
// enough to read, short enough that it never masquerades as live state.
const noticeTTL = 6 * time.Second

// noticeLevel decides how a notice is painted.
type noticeLevel int

const (
	noticeInfo noticeLevel = iota
	noticeGood
	noticeBad
)

type notice struct {
	text  string
	level noticeLevel
	at    time.Time
}

// run turns one action into a tea.Cmd. The nil check happens here so every key
// gets a notice rather than silence.
func (m Model) run(verb string, fn func() error) (Model, tea.Cmd) {
	if m.actions == nil || fn == nil {
		m.notice = notice{text: "no daemon to ask — start it with `synckeeper watch` or `service install`", level: noticeBad, at: m.now()}
		return m, nil
	}
	if m.pending != "" {
		// Serialising is deliberate: the control protocol is one request per
		// connection, and a second sync-now while the first is in flight would
		// only confuse the report.
		m.notice = notice{text: "already asking the daemon to " + m.pending + "…", level: noticeInfo, at: m.now()}
		return m, nil
	}
	m.pending = verb
	m.notice = notice{text: "asking the daemon to " + verb + "…", level: noticeInfo, at: m.now()}
	return m, func() tea.Msg { return actionDoneMsg{verb: verb, err: fn()} }
}

// applyActionResult records the outcome and refreshes, so the panels reflect
// what just happened instead of waiting out the interval.
func (m Model) applyActionResult(msg actionDoneMsg) Model {
	m.pending = ""
	switch {
	case msg.err != nil:
		m.notice = notice{text: "could not " + msg.verb + ": " + msg.err.Error(), level: noticeBad, at: m.now()}
	default:
		m.notice = notice{text: doneText(msg.verb), level: noticeGood, at: m.now()}
	}
	if m.refresh != nil {
		m.snap = m.refresh()
		m.clampOffset()
	}
	return m
}

// doneText reports what actually happened, and — for a sync — what still has
// not. A held deletion is released only by an explicit `--confirm-deletes` on
// the command line: the daemon never self-confirms a mass delete (spec §8.1),
// and a keystroke must not either.
func doneText(verb string) string {
	switch verb {
	case verbSync:
		return "sync requested"
	case verbPause:
		return "paused — automatic syncing is suspended"
	case verbResume:
		return "resumed — automatic syncing is active"
	case verbReload:
		return "config reloaded (identity fields need a daemon restart)"
	default:
		return verb + " done"
	}
}

// The verbs, shared by the keys, the notices and the tests.
const (
	verbSync   = "sync now"
	verbPause  = "pause"
	verbResume = "resume"
	verbReload = "reload the config"
)

// visibleNotice returns the notice to draw, or false once it has expired.
func (m Model) visibleNotice() (notice, bool) {
	if m.notice.text == "" {
		return notice{}, false
	}
	if m.pending == "" && m.now().Sub(m.notice.at) > noticeTTL {
		return notice{}, false
	}
	return m.notice, true
}
