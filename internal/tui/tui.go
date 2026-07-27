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
// Refresh and Clock, so tests drive the model with a canned snapshot and a
// pinned instant.
type Model struct {
	snap     Snapshot
	view     int
	showHelp bool
	width    int
	height   int
	color    bool

	// Activity view state (U3).
	offset    int    // first visible row of the filtered list
	dirFilter string // "" = all, else an activity Source
	query     string // incremental search over path, detail and kind
	searching bool   // the query line is accepting keystrokes

	// Action state (U4).
	actions *Actions
	pending string // an action in flight, "" when idle
	notice  notice // the last result line, self-expiring

	refresh  func() Snapshot
	clock    func() time.Time
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

	// Actions are the daemon commands the dashboard may ask for; nil means the
	// keys still respond but report that there is nothing to ask.
	Actions *Actions

	// Clock is the instant a frame renders against; nil means time.Now. It is
	// separate from the snapshot's own Now so relative times keep advancing
	// between database reads (U3) — and so a test can pin every frame.
	Clock func() time.Time

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
		actions:  o.Actions,
		clock:    o.Clock,
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

// Two tickers, deliberately: tickMsg re-reads the database at the refresh
// interval, while frameMsg only redraws. The countdown and the "2m ago" column
// therefore keep moving between reads — at --interval 30s the clock must not
// freeze for 30 seconds — without querying SQLite eight times a second.
type tickMsg time.Time
type frameMsg time.Time

// FrameInterval is the redraw cadence. Everything the dashboard shows is at
// second granularity, so this only has to be fast enough that a ticking second
// never looks stuck.
const FrameInterval = 250 * time.Millisecond

func tickAfter(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func frameAfter(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return frameMsg(t) })
}

// Init starts the data and frame tickers.
func (m Model) Init() tea.Cmd {
	if m.refresh == nil {
		return nil
	}
	return tea.Batch(tickAfter(m.interval), frameAfter(FrameInterval))
}

// Update handles keys, resizes, and both tickers. It is deliberately total: an
// unknown message leaves the model untouched.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampOffset()
		return m, nil

	case tickMsg:
		if m.refresh == nil {
			return m, nil
		}
		m.snap = m.refresh()
		m.clampOffset()
		return m, tickAfter(m.interval)

	case frameMsg:
		if m.refresh == nil {
			return m, nil
		}
		// Nothing to recompute: the render reads the clock itself.
		return m, frameAfter(FrameInterval)

	case actionDoneMsg:
		return m.applyActionResult(msg), nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// now is the instant the current frame renders against.
func (m Model) now() time.Time {
	if m.clock != nil {
		return m.clock()
	}
	return time.Now()
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// While the search line is open it swallows most keys, so a query can
	// contain "q" without quitting the dashboard.
	if m.searching {
		return m.handleSearchKey(msg)
	}

	switch msg.String() {
	case "q", "ctrl+c", "esc":
		return m, tea.Quit
	case "1":
		m.selectView(ViewOverview)
	case "2":
		m.selectView(ViewActivity)
	case "3":
		m.selectView(ViewInfo)
	case "tab", "right":
		m.selectView((m.view + 1) % numViews)
	case "shift+tab", "left":
		m.selectView((m.view + numViews - 1) % numViews)
	case "?":
		m.showHelp = !m.showHelp
	// No manual-refresh key: the view re-reads itself, and a lowercase `r`
	// sitting next to `R` (reload the daemon's config) read as a pair when it
	// was nothing of the kind — Max, 2026-07-27, decisions.md.

	// Daemon actions (U4) — each is the control call the matching CLI command
	// already makes. `s` never confirms a held mass deletion: that stays an
	// explicit `sync --confirm-deletes` on the command line.
	case "s":
		return m.runAction(verbSync)
	case "p":
		return m.runAction(verbPause)
	case "P":
		return m.runAction(verbResume)
	case "R":
		return m.runAction(verbReload)

	// Scrolling — only the activity view has more rows than fit.
	case "j", "down":
		m.scroll(1)
	case "k", "up":
		m.scroll(-1)
	case "pgdown", "ctrl+f", " ":
		m.scroll(m.activityRowBudget())
	case "pgup", "ctrl+b":
		m.scroll(-m.activityRowBudget())
	case "g", "home":
		m.offset = 0
	case "G", "end":
		m.offset = max(0, len(m.filteredActivity())-m.activityRowBudget())

	// Filtering and search.
	case "f":
		m.view = ViewActivity
		m.showHelp = false
		m.dirFilter = nextDirFilter(m.dirFilter)
		m.offset = 0
	case "/":
		m.view = ViewActivity
		m.showHelp = false
		m.searching = true
	case "c":
		m.dirFilter, m.query, m.offset = "", "", 0
	}
	return m, nil
}

// handleSearchKey drives the incremental search line. Enter keeps the query and
// closes the line; Esc abandons it, restoring what was showing before.
func (m Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.searching = false
	case tea.KeyEsc:
		m.searching, m.query, m.offset = false, "", 0
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyBackspace:
		if r := []rune(m.query); len(r) > 0 {
			m.query = string(r[:len(r)-1])
			m.offset = 0
		}
	case tea.KeyRunes, tea.KeySpace:
		m.query += string(msg.Runes)
		if msg.Type == tea.KeySpace {
			m.query += " "
		}
		m.offset = 0
	}
	return m, nil
}

// runAction dispatches one of the four daemon commands.
func (m Model) runAction(verb string) (tea.Model, tea.Cmd) {
	var fn func() error
	if m.actions != nil {
		switch verb {
		case verbSync:
			fn = m.actions.SyncNow
		case verbPause:
			fn = m.actions.Pause
		case verbResume:
			fn = m.actions.Resume
		case verbReload:
			fn = m.actions.Reload
		}
	}
	next, cmd := m.run(verb, fn)
	return next, cmd
}

func (m *Model) selectView(v int) {
	if v != m.view {
		m.offset = 0
	}
	m.view, m.showHelp = v, false
	m.clampOffset()
}

func (m *Model) scroll(delta int) {
	m.offset += delta
	m.clampOffset()
}

func (m *Model) clampOffset() {
	limit := max(0, len(m.filteredActivity())-m.activityRowBudget())
	if m.offset > limit {
		m.offset = limit
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

// dirFilters is the cycle order for `f`: everything, then each direction the
// recorder actually writes (statedb Activity.Source), then errors — which carry
// no source and are the rows a user hunts for most often.
var dirFilters = []string{"", "local", "remote", "conflict", "error"}

func nextDirFilter(current string) string {
	for i, f := range dirFilters {
		if f == current {
			return dirFilters[(i+1)%len(dirFilters)]
		}
	}
	return ""
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
