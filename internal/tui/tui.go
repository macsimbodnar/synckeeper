// Package tui is the live dashboard behind `synckeeper status` on a terminal
// (W15). It is a strict read-only client: it renders a status.Snapshot the
// caller hands it, holds no lock, opens no writable database, and performs no
// sync work of its own.
//
// Update and View are pure functions of the model, which is the whole reason
// this package can be tested without a terminal: every view state is a golden
// string produced by calling View() on a hand-built Model.
package tui

import (
	"io"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/macsimbodnar/synckeeper/internal/status"
)

// View identifiers, in the order the number keys select them.
const (
	ViewOverview = iota
	ViewActivity
	ViewInfo
	numViews
)

// InfoRow is one label/value line of the static view. The caller builds these
// from `info`'s own gatherer, so the dashboard cannot drift from what
// `synckeeper info` reports: this package never resolves a path itself.
type InfoRow struct {
	Label string
	Value string
	Note  string // parenthetical: a warning or clarification, may be empty
}

// Snapshot is everything one frame renders.
type Snapshot struct {
	Status status.Snapshot
	Info   []InfoRow
}

// Model is the dashboard state. Everything that reads the world is behind
// Refresh, so tests drive the model with a canned snapshot.
type Model struct {
	snap     Snapshot
	view     int
	showHelp bool
	width    int
	height   int
	color    bool

	refresh  func() Snapshot
	interval time.Duration
}

// Options configures a Model. Refresh is required for a live run; tests leave
// it nil and set Snapshot directly.
type Options struct {
	Snapshot Snapshot
	Refresh  func() Snapshot
	Interval time.Duration
	Color    bool
	Width    int
	Height   int

	// Input/Output are terminal seams: nil means the real terminal (and the
	// alternate screen). A test hands Run a reader of keystrokes and a buffer,
	// so the whole program — event loop included — runs without a tty.
	Input  io.Reader
	Output io.Writer
}

// New builds a Model. Width/Height are placeholders until the terminal reports
// its real size; the defaults are a conventional 80x24 so a first frame drawn
// before that message still looks sane.
func New(o Options) Model {
	m := Model{
		snap:     o.Snapshot,
		refresh:  o.Refresh,
		interval: o.Interval,
		color:    o.Color,
		width:    o.Width,
		height:   o.Height,
	}
	if m.interval <= 0 {
		m.interval = time.Second
	}
	if m.width <= 0 {
		m.width = 80
	}
	if m.height <= 0 {
		m.height = 24
	}
	return m
}

type tickMsg time.Time

func tickAfter(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Init starts the refresh ticker.
func (m Model) Init() tea.Cmd {
	if m.refresh == nil {
		return nil
	}
	return tickAfter(m.interval)
}

// Update handles keys, resizes, and the refresh tick. It is deliberately total:
// an unknown message leaves the model untouched.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tickMsg:
		if m.refresh == nil {
			return m, nil
		}
		m.snap = m.refresh()
		return m, tickAfter(m.interval)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c", "esc":
		return m, tea.Quit
	case "1":
		m.view, m.showHelp = ViewOverview, false
	case "2":
		m.view, m.showHelp = ViewActivity, false
	case "3":
		m.view, m.showHelp = ViewInfo, false
	case "tab", "right", "l":
		m.view, m.showHelp = (m.view+1)%numViews, false
	case "shift+tab", "left", "h":
		m.view, m.showHelp = (m.view+numViews-1)%numViews, false
	case "?":
		m.showHelp = !m.showHelp
	case "r":
		// A manual redraw: cheap, and it is what a user reaches for when they
		// do not want to wait out the interval. `R` (reload config) and the
		// other daemon actions arrive with W15-U4.
		if m.refresh != nil {
			m.snap = m.refresh()
		}
	}
	return m, nil
}

// ViewName is the human label of a view, used by the footer and by tests.
func ViewName(v int) string {
	switch v {
	case ViewActivity:
		return "activity"
	case ViewInfo:
		return "info"
	default:
		return "overview"
	}
}
