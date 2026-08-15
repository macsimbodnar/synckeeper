package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/macsimbodnar/synckeeper/internal/statedb"
	"github.com/macsimbodnar/synckeeper/internal/status"
)

// narrowWidth is where the two-column layout stops fitting and the panels
// stack instead. Below it, a side-by-side layout truncates both halves into
// uselessness, which is worse than scrolling.
const narrowWidth = 70

// chromeRows is what the frame spends around the body: the header, the identity
// strip, the blank spacer and the footer. The rest is the body's height budget,
// and the body is padded to exactly that many rows — a frame is always the full
// height of the terminal, so the footer sits on the last line instead of
// floating half-way up the screen under a short panel.
const chromeRows = 4

// View renders one frame. Pure: same model, same string, no clock and no
// terminal involved — which is how every state below is golden-tested.
func (m Model) View() string {
	t := theme{color: m.color}
	w := m.width
	// Every relative time in the frame is measured from this instant, not from
	// when the snapshot was read: at a long --interval the countdown must keep
	// moving between database reads (U3). The snapshot's own Now stays as the
	// record of when it was gathered.
	m.snap.Status.Now = m.now()
	notice, hasNotice := m.noticeLine(t, w)
	body := m.fitBody(t, m.body(t, w), hasNotice)

	var b strings.Builder
	b.WriteString(m.header(t, w))
	b.WriteByte('\n')
	b.WriteString(m.identity(t, w))
	b.WriteByte('\n')
	if hasNotice {
		b.WriteString(notice)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(body)
	b.WriteByte('\n')
	b.WriteString(m.footer(t, w))
	// No trailing newline: the footer is the terminal's last line, and one more
	// newline would cost a row — the frame would be a line short of the bottom.
	return b.String()
}

// fitBody sizes the body to exactly the rows the frame has for it, in both
// directions.
//
// Padding is the visible half: a body shorter than its budget used to leave the
// footer wherever the panels happened to end, so on a tall window the tab bar
// floated near the top with a screen of blank underneath it.
//
// Clamping is the height guard: whatever a panel decided to draw, the finished
// frame never emits more lines than the terminal has. Every budget above is
// computed in *rows*, so any value that renders taller than one row would
// scroll the header away — which is exactly what a multi-line Drive error did
// before those values were flattened. The flattening is the fix; this is the
// invariant that keeps the next such value from reaching the screen, and a test
// asserts it for every state.
func (m Model) fitBody(t theme, body string, hasNotice bool) string {
	allowed := m.bodyRows(hasNotice)
	lines := strings.Split(body, "\n")
	switch {
	case allowed < 1: // a window too short even for the chrome; clamp handled above it
		return body
	case len(lines) > allowed:
		kept := lines[:allowed]
		kept[allowed-1] = t.muted(truncate("… the window is too short to draw the rest", m.width))
		return strings.Join(kept, "\n")
	default:
		return body + strings.Repeat("\n", allowed-len(lines))
	}
}

// bodyRows is how many terminal rows the body occupies: everything the chrome
// does not take. It is the raw figure — bodyBudget floors it for the panels.
func (m Model) bodyRows(hasNotice bool) int {
	rows := m.height - chromeRows
	if hasNotice {
		rows--
	}
	return rows
}

// noticeLine reports the result of the last daemon action, painted by outcome
// and self-expiring so it can never be mistaken for live state.
func (m Model) noticeLine(t theme, w int) (string, bool) {
	n, ok := m.visibleNotice()
	if !ok {
		return "", false
	}
	glyph, paintFn := "·", t.muted
	switch n.level {
	case noticeGood:
		glyph, paintFn = "✓", t.good
	case noticeBad:
		glyph, paintFn = "✗", t.bad
	}
	return paintFn(truncate(glyph+" "+n.text, w)), true
}

func (m Model) body(t theme, w int) string {
	if m.showHelp {
		return m.helpBody(t, w)
	}
	switch m.view {
	case ViewActivity:
		return m.activityBody(t, w)
	case ViewInfo:
		return m.infoBody(t, w)
	default:
		return m.overviewBody(t, w)
	}
}

// header: the one line that must answer "is it working?" at a glance.
func (m Model) header(t theme, w int) string {
	s := m.snap.Status
	ds := s.Daemon

	var glyph, word string
	var paintFn func(string) string
	switch s.State {
	case status.StateRunning:
		if ds.Paused {
			glyph, word, paintFn = "‖", "paused", t.warn
		} else {
			glyph, word, paintFn = "●", "running", t.good
		}
	case status.StateStale:
		glyph, word, paintFn = "▲", "NOT RUNNING", t.bad
	case status.StateStopped:
		glyph, word, paintFn = "○", "stopped", t.muted
	default:
		glyph, word, paintFn = "○", "never started", t.muted
	}

	// Segments are kept in plain form so the header can be trimmed to width
	// without ever cutting through an escape sequence: whole segments are
	// dropped from the right, then what survives is painted.
	type seg struct {
		text  string
		paint func(string) string
	}
	segs := []seg{{glyph + " " + word, paintFn}}
	if s.State == status.StateRunning {
		mode := ds.Mode
		if ds.Paused {
			mode = "auto-sync suspended"
		} else if b := m.snap.Live.backendLabel(); b != "" && mode != "" {
			// The daemon's own answer, so this cannot claim "watching" while the
			// loop is polling (W3-adv-3's honesty bug, kept out of the dashboard).
			mode += " (" + b + ")"
		}
		if mode != "" {
			segs = append(segs, seg{mode, t.muted})
		}
		if ds.PID > 0 {
			segs = append(segs, seg{fmt.Sprintf("pid %d", ds.PID), t.muted})
		}
		if ds.StartedAt > 0 {
			segs = append(segs, seg{"up " + status.Dur(s.Now.Sub(unix(ds.StartedAt))), t.muted})
		}
	} else if ds.LastHeartbeatAt > 0 {
		segs = append(segs, seg{"last seen " + status.Ago(s.Now, ds.LastHeartbeatAt), t.muted})
	}
	if ds.GuardBlocked {
		segs = append(segs, seg{"GUARD BLOCKED", t.bad})
	}

	const brand = "synckeeper"
	clockText := s.Now.Format("15:04:05")
	// The clock is the first thing sacrificed, then trailing segments.
	budget := w - visibleLen(brand) - 2
	clock := ""
	if budget-visibleLen(clockText)-1 > 0 {
		budget -= visibleLen(clockText) + 1
		clock = t.muted(clockText)
	}

	var kept []string
	used := 0
	for i, sg := range segs {
		add := visibleLen(sg.text)
		if i > 0 {
			add += 3 // " · "
		}
		if used+add > budget {
			break
		}
		used += add
		kept = append(kept, sg.paint(sg.text))
	}
	if len(kept) == 0 { // a window too narrow even for the state word
		return truncate(t.bold(brand), w)
	}

	left := t.bold(brand) + "  " + strings.Join(kept, t.muted(" · "))
	if clock == "" {
		return left
	}
	return joinEnds(left, clock, w)
}

// identity: the static context that must never leave the screen — which machine
// this is, and which pair of folders it keeps.
func (m Model) identity(t theme, w int) string {
	s := m.snap.Status
	name := s.MachineName
	if name == "" {
		name = "(unnamed machine)"
	}
	line := name + "  ·  " + s.SyncDir + "  ⇄  " + fmt.Sprintf("%q", s.DriveFolder)
	return t.muted(truncate(line, w))
}

func (m Model) overviewBody(t theme, w int) string {
	s := m.snap.Status

	var out []string
	if w >= narrowWidth {
		colW := (w - 2) / 2
		cycle := m.cyclePanel(t, colW)
		totals := m.totalsPanel(t, colW)
		left := t.rule("cycle", colW) + "\n" + strings.Join(indent(cycle), "\n")
		right := t.rule("totals", colW) + "\n" + strings.Join(indent(totals), "\n")
		out = append(out, lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.NewStyle().Width(colW+2).Render(left),
			lipgloss.NewStyle().Width(colW).Render(right)))
	} else {
		cycle := m.cyclePanel(t, w)
		totals := m.totalsPanel(t, w)
		out = append(out, t.rule("cycle", w))
		out = append(out, indent(cycle)...)
		out = append(out, t.rule("totals", w))
		out = append(out, indent(totals)...)
	}

	if att := m.attention(t, w); len(att) > 0 {
		out = append(out, "", t.rule("attention", w))
		out = append(out, indent(att)...)
	}

	rows := m.bodyBudget() - countLines(out) - 2
	if rows > 0 && len(s.Activity) > 0 {
		out = append(out, "", t.rule("activity", w))
		out = append(out, indent(m.rowsFor(t, s.Activity, w-1, rows))...)
	}
	return strings.Join(out, "\n")
}

func (m Model) cyclePanel(t theme, colW int) []string {
	s := m.snap.Status
	ds := s.Daemon
	if s.State != status.StateRunning {
		// One column wide, so this stays a label; the actionable hint lives in
		// the full-width attention panel below.
		msg := "not running"
		if s.State == status.StateStale {
			msg = "no heartbeat — looks crashed"
		}
		return []string{t.muted(truncate(msg, max(1, colW-1)))}
	}

	live := m.snap.Live
	var rows []string

	// A cycle in flight is the most interesting thing on the screen, so it
	// takes the top line — and it is only knowable from the daemon's memory.
	if live.Have && live.CycleRunning {
		// The stage names what is being waited on, which is the useful part of a
		// long cycle: "checking Drive · 45s" points at the network, not at us.
		what := live.Stage
		if what == "" {
			what = "now"
		}
		if live.StageActions > 0 {
			what += fmt.Sprintf(" · %s actions", comma(live.StageActions))
		}
		rows = append(rows, field(t, "syncing", what+" · "+status.Dur(live.CycleElapsed), colW, t.good))
	}

	switch {
	case live.Have && live.WakePending:
		// A local change is in the debounce window: the poll deadline is
		// irrelevant, the next cycle is milliseconds away.
		wake := "local change — syncing"
		if n := live.PendingChanges; n > 1 {
			wake = fmt.Sprintf("%d local changes — syncing", n)
		}
		rows = append(rows, field(t, "next sync", wake, colW, t.good))
	case live.Have && live.TickDue:
		rows = append(rows, field(t, "next poll", "due now", colW, nil))
	case live.Have && !live.NextTickAt.IsZero():
		// No "≈": this is the poll timer's real deadline, from the daemon.
		rows = append(rows, field(t, "next poll", status.Until(s.Now, live.NextTickAt.Unix()), colW, nil))
	case ds.NextPollAt > 0:
		// DB-only fallback. "≈" is not decoration: NextPollAt is written at
		// cycle end while the poll ticker runs independently and any local
		// change pre-empts it, so the figure is an estimate.
		rows = append(rows, field(t, "next poll", "≈ "+status.Until(s.Now, ds.NextPollAt), colW, nil))
	default:
		rows = append(rows, field(t, "next poll", "unknown", colW, t.muted))
	}
	if ds.LastSyncAt > 0 {
		rows = append(rows, field(t, "last sync", status.Ago(s.Now, ds.LastSyncAt), colW, nil))
	} else {
		rows = append(rows, field(t, "last sync", "never", colW, t.muted))
	}
	if cs, ok := status.ParseCycle(ds.LastCycleJSON); ok {
		summary := fmt.Sprintf("%d actions · %d ok", cs.Actions, cs.Executed)
		paint := func(s string) string { return s }
		if cs.Failed > 0 {
			summary += fmt.Sprintf(" · %d failed", cs.Failed)
			paint = t.bad // a failure colours the whole line rather than a fragment
		}
		rows = append(rows, field(t, "last cycle", summary, colW, paint))
		rows = append(rows, field(t, "duration", fmt.Sprintf("%d ms", cs.DurationMS), colW, nil))
	} else {
		rows = append(rows, field(t, "last cycle", "none recorded", colW, t.muted))
	}
	return rows
}

func (m Model) totalsPanel(t theme, colW int) []string {
	s := m.snap.Status
	quarantine := fmt.Sprintf("%s files", comma(s.QFiles))
	if s.QFiles > 0 {
		quarantine += fmt.Sprintf(" (%s bytes)", comma(int(s.QBytes)))
	}
	bin, binPaint := s.BinDest, func(s string) string { return s }
	if !s.BinAvailable {
		bin, binPaint = "UNAVAILABLE → quarantine", t.warn
	}
	return []string{
		field(t, "tracked", comma(s.Items), colW, nil),
		field(t, "pending ops", comma(s.Pending), colW, nil),
		field(t, "quarantine", quarantine, colW, nil),
		field(t, "system bin", bin, colW, binPaint),
	}
}

// attention collects everything a user would want to be told without asking.
// Empty means the panel is not drawn at all: a permanently visible "all good"
// box trains people to ignore the space where the warnings appear.
func (m Model) attention(t theme, w int) []string {
	s := m.snap.Status
	var out []string
	if s.Daemon.GuardBlocked {
		out = append(out, t.bad("⚠ guard BLOCKED")+" — "+truncate(s.Daemon.GuardReason, max(0, w-20)))
		out = append(out, t.muted("  release it with `synckeeper sync --confirm-deletes` on this machine"))
	}
	// The credentials line comes first and names the cure: when Google refuses
	// the refresh token, every other line on the screen is a consequence, and
	// the raw OAuth error underneath tells the user nothing they can act on.
	if s.TokenRejected {
		out = append(out, t.bad("⚠ credentials expired")+" — run `synckeeper login`; syncing is stopped until you do")
	}
	if s.Daemon.LastError != "" {
		out = append(out, t.bad("⚠ last error")+" — "+truncate(s.Daemon.LastError, max(0, w-18)))
	}
	if !s.BinAvailable {
		out = append(out, t.warn("⚠ no system bin")+" — "+truncate(status.SystemBinLine(false, s.BinDest), max(0, w-20)))
	}
	switch s.State {
	case status.StateStale:
		out = append(out, t.bad("⚠ the daemon stopped sending heartbeats")+" — it may have crashed; check the log")
	case status.StateStopped, status.StateNeverRun:
		out = append(out, t.warn("⚠ nothing is syncing")+" — run `synckeeper watch`, or `service install` to sync in the background")
	}
	if !s.TokenOK {
		out = append(out, t.warn("⚠ no saved credentials")+" — run `synckeeper login`")
	}
	if s.AutostartErr == nil && !s.Autostart.Installed {
		out = append(out, t.muted("· autostart not installed — `synckeeper service install`"))
	}
	return out
}

func (m Model) activityBody(t theme, w int) string {
	all := m.snap.Status.Activity
	shown := m.filteredActivity()

	label := fmt.Sprintf("activity — %d entries", len(all))
	if f := filterLabel(m.dirFilter, m.query); f != "" {
		label = fmt.Sprintf("activity — %d of %d · %s", len(shown), len(all), f)
	}
	out := []string{t.rule(label, w)}

	if m.searching {
		out = append(out, indentOne(t.accent("/")+m.query+t.muted("▏  enter keep · esc cancel")))
	}

	budget := m.activityRowBudget()
	switch {
	case len(all) == 0:
		out = append(out, indentOne(t.muted("nothing recorded yet")))
	case len(shown) == 0:
		out = append(out, indentOne(t.muted("no entry matches — `c` clears the filter")))
	default:
		window := shown[min(m.offset, len(shown)):]
		out = append(out, indent(m.rowsFor(t, window, w-1, budget))...)
		if more := len(shown) - m.offset - budget; more > 0 {
			out = append(out, indentOne(t.muted(fmt.Sprintf("… %d older (j/k scroll · G end)", more))))
		} else if m.offset > 0 {
			out = append(out, indentOne(t.muted("— end · g back to newest")))
		}
	}
	return strings.Join(out, "\n")
}

// activityRowBudget is how many activity rows the activity view can draw, after
// its heading and (when open) the search line.
func (m Model) activityRowBudget() int {
	budget := m.bodyBudget() - 2 // heading + the "… N older" footer line
	if m.searching {
		budget--
	}
	return max(1, budget)
}

// rowsFor renders activity rows newest-first, one line each, clipped to limit.
func (m Model) rowsFor(t theme, acts []statedb.Activity, w, limit int) []string {
	s := m.snap.Status
	if limit <= 0 {
		return nil
	}
	if len(acts) > limit {
		acts = acts[:limit]
	}
	// Columns are dropped, never squeezed past legibility: a row that cannot
	// hold the direction label loses it (the glyph still carries the
	// direction), and a row that cannot hold the kind loses that too. The path
	// tail — the file name — is the last thing to go.
	const tsW, dirW, kindW, minPathW = 9, 13, 9, 12
	full := w - (tsW + dirW + kindW + 3)
	compact := w - (tsW + 1 + kindW + 3)
	layout := "minimal"
	pathW := w - 2
	switch {
	case full >= minPathW:
		layout, pathW = "full", full
	case compact >= minPathW:
		layout, pathW = "compact", compact
	}

	rows := make([]string, 0, len(acts))
	for _, a := range acts {
		glyph, paintFn := activityGlyph(t, a.Source)
		dir := status.DirectionLabel(a.Source)
		if dir == "" {
			dir = "—"
		}
		text := a.RelPath
		// A row with no path is a message, not a file: an error or a cycle
		// summary. Cutting it from the left — right for a path, whose tail is
		// the file name — throws away the sentence that says what went wrong
		// and keeps the JSON tail that says nothing.
		cut := truncatePath
		if a.Detail != "" {
			if text == "" {
				text, cut = a.Detail, truncate
			} else {
				text += " " + a.Detail
			}
		}
		if a.Count > 1 {
			// A folded row's count is the news in it — "this has been failing 288
			// times" — so it goes on the end the width cut keeps: in front of a
			// message (cut from the right), behind a path (cut from the left).
			if a.RelPath == "" {
				text = "×" + comma(a.Count) + " " + text
			} else {
				text += " ×" + comma(a.Count)
			}
		}
		switch layout {
		case "full":
			rows = append(rows, fmt.Sprintf("%s %s %s %s",
				pad(status.Ago(s.Now, a.TS), tsW),
				paintFn(pad(glyph+" "+dir, dirW)),
				pad(a.Kind, kindW),
				cut(text, pathW)))
		case "compact":
			rows = append(rows, fmt.Sprintf("%s %s %s %s",
				pad(status.Ago(s.Now, a.TS), tsW),
				paintFn(glyph),
				pad(truncate(a.Kind, kindW), kindW),
				cut(text, pathW)))
		default:
			rows = append(rows, paintFn(glyph)+" "+cut(text, pathW-2))
		}
	}
	return rows
}

func activityGlyph(t theme, source string) (string, func(string) string) {
	switch source {
	case "local":
		return "↑", func(s string) string { return t.paint(colLocal, s) }
	case "remote":
		return "↓", func(s string) string { return t.paint(colRemote, s) }
	case "conflict":
		return "⇅", func(s string) string { return t.paint(colConflct, s) }
	default:
		return "!", t.bad
	}
}

func (m Model) infoBody(t theme, w int) string {
	rows := m.snap.Info
	if len(rows) == 0 {
		return t.rule("info", w) + "\n" + indentOne(t.muted("no static information available"))
	}
	labelW := 0
	for _, r := range rows {
		if n := visibleLen(r.Label); n > labelW {
			labelW = n
		}
	}
	if labelW > w/3 {
		labelW = max(6, w/3)
	}

	out := []string{t.rule("info", w)}
	budget := m.bodyBudget() - 1
	for i, r := range rows {
		if i >= budget {
			out = append(out, indentOne(t.muted(fmt.Sprintf("… %d more (widen or lengthen the window)", len(rows)-i))))
			break
		}
		if r.Label == "" && r.Value == "" { // a spacer row
			out = append(out, "")
			continue
		}
		// The note is budgeted before the value, and never takes more than half
		// of what is left: a long "readable by others; chmod 600" must not push
		// the path it is talking about off the screen.
		avail := w - 1 - labelW - 2
		note := ""
		if r.Note != "" {
			note = " (" + r.Note + ")"
			if visibleLen(note) > avail/2 {
				note = truncate(note, max(0, avail/2))
			}
		}
		valueW := max(1, avail-visibleLen(note))
		line := t.muted(pad(truncate(r.Label, labelW), labelW)) + "  " + truncatePath(r.Value, valueW)
		if note != "" {
			line += t.warn(note)
		}
		out = append(out, indentOne(line))
	}
	return strings.Join(out, "\n")
}

func (m Model) helpBody(t theme, w int) string {
	rows := [][2]string{
		{"1 / 2 / 3", "overview · activity · info"},
		{"tab / ←→", "cycle through the views"},
		{"j / k", "scroll the activity list (↑↓ too)"},
		{"pgup/pgdn", "scroll a page (space pages down)"},
		{"g / G", "jump to the newest / oldest entry"},
		{"f", "filter activity: all → local→drive → drive→local → conflict → errors"},
		{"/", "search paths, details and kinds; enter keeps it, esc cancels"},
		{"c", "clear the filter and the search"},
		{"s", "sync now (never confirms a held mass delete — see below)"},
		{"p / P", "pause / resume automatic syncing"},
		{"R", "reload config.toml in the daemon (asks the daemon)"},
		{"?", "toggle this help"},
		{"q / ctrl+c", "quit"},
	}
	out := []string{t.rule("keys", w)}
	for _, r := range rows {
		out = append(out, indentOne(t.accent(pad(r[0], 12))+t.muted(r[1])))
	}
	out = append(out, "", indentOne(t.muted("the dashboard only reads: it holds no lock and never writes to the database.")))
	out = append(out, indentOne(t.muted("a deletion held by the mass-delete guard is released only by")))
	out = append(out, indentOne(t.muted("`synckeeper sync --confirm-deletes` — never by a keystroke here.")))
	return strings.Join(out, "\n")
}

func (m Model) footer(t theme, w int) string {
	var tabs []string
	for v := ViewOverview; v < numViews; v++ {
		label := fmt.Sprintf("%d %s", v+1, ViewName(v))
		if v == m.view && !m.showHelp {
			tabs = append(tabs, t.bold(t.accent(label)))
		} else {
			tabs = append(tabs, t.muted(label))
		}
	}
	left := strings.Join(tabs, t.muted(" · "))
	right := t.muted("s sync · p/P pause · ? help · q quit")
	switch {
	case m.pending != "":
		right = t.warn("… " + m.pending)
	case m.view == ViewActivity && !m.showHelp:
		right = t.muted("f filter · / search · j/k scroll · ? help · q quit")
	}
	return joinEnds(left, right, w)
}

// bodyBudget is how many lines the body may draw. A visible notice costs one.
// The floor keeps a panel from computing a negative window on a tiny terminal;
// fitBody cuts whatever overflows.
func (m Model) bodyBudget() int {
	_, hasNotice := m.visibleNotice()
	return max(3, m.bodyRows(hasNotice))
}

// joinEnds puts left and right on one line, right-aligned, dropping the right
// half when the width cannot hold both.
func joinEnds(left, right string, w int) string {
	gap := w - visibleLenANSI(left) - visibleLenANSI(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// visibleLenANSI measures a string that may already carry escape codes from the
// theme. lipgloss.Width does exactly this and is correct for wide runes too.
func visibleLenANSI(s string) int { return lipgloss.Width(s) }

// field renders one `label  value` line. The value arrives **plain** and is
// truncated before it is painted: paint-then-cut would slice through an escape
// sequence, the trap the header already avoids by composing plain segments.
// budget is the column width the line must fit.
func field(t theme, label, value string, budget int, paint func(string) string) string {
	const labelW = 11
	if paint == nil {
		paint = func(s string) string { return s }
	}
	room := budget - labelW - 2 // label, a space, and the one-column indent
	return t.muted(pad(label, labelW)) + " " + paint(truncate(value, max(1, room)))
}

func indent(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = " " + l
	}
	return out
}

func indentOne(line string) string { return " " + line }

func countLines(blocks []string) int {
	n := 0
	for _, b := range blocks {
		n += strings.Count(b, "\n") + 1
	}
	return n
}

func unix(sec int64) time.Time { return time.Unix(sec, 0) }
