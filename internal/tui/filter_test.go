package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// busySnapshot has more activity than any window can show, with a known mix of
// directions and one error row, so scrolling and filtering have something to
// bite on.
func busySnapshot(ref time.Time) Snapshot {
	s := testSnapshot(ref)
	acts := make([]statedb.Activity, 0, 40)
	for i := 0; i < 40; i++ {
		a := statedb.Activity{
			TS:      ref.Add(-time.Duration(65+i*61) * time.Second).Unix(),
			Kind:    "upload",
			RelPath: fmt.Sprintf("Photos/2026/img_%04d.raf", 8800+i),
			Source:  "local",
		}
		switch i % 4 {
		case 1:
			a.Kind, a.Source, a.RelPath = "download", "remote", fmt.Sprintf("Notes/note-%02d.md", i)
		case 2:
			a.Kind, a.Source, a.RelPath = "conflict", "conflict", fmt.Sprintf("Budget-%02d.xlsx", i)
		case 3:
			a.Kind, a.Source, a.RelPath = "error", "", ""
			a.Detail = fmt.Sprintf("refresh remote state: attempt %d failed", i)
		}
		acts = append(acts, a)
	}
	s.Status.Activity = acts
	return s
}

func busyModel(view int) Model {
	m := New(Options{Snapshot: busySnapshot(testRef()), Width: 100, Height: 20, Clock: testRef})
	m.view = view
	return m
}

func press(m Model, key string) Model {
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return next.(Model)
}

func pressKey(m Model, k tea.KeyType) Model {
	next, _ := m.Update(tea.KeyMsg{Type: k})
	return next.(Model)
}

// TestScrollingStaysInBounds: scrolling must never run past either end, and the
// view must say when there is more below.
func TestScrollingStaysInBounds(t *testing.T) {
	m := busyModel(ViewActivity)
	if !strings.Contains(m.View(), "older") {
		t.Error("a list longer than the window must say how many entries are below")
	}

	// Scrolling up at the top is a no-op, not a negative offset.
	up := press(m, "k")
	if up.offset != 0 {
		t.Errorf("offset went to %d scrolling up from the top", up.offset)
	}

	down := press(m, "j")
	if down.offset != 1 {
		t.Errorf("j moved to offset %d, want 1", down.offset)
	}

	// G jumps to the end; further scrolling cannot pass it.
	end := press(m, "G")
	limit := len(end.filteredActivity()) - end.activityRowBudget()
	if end.offset != limit {
		t.Errorf("G left offset %d, want %d", end.offset, limit)
	}
	for i := 0; i < 5; i++ {
		end = press(end, "j")
	}
	if end.offset != limit {
		t.Errorf("scrolling past the end reached offset %d, want %d", end.offset, limit)
	}
	if strings.Contains(end.View(), "older") {
		t.Error("at the end the view should not claim there are older entries")
	}
	if !strings.Contains(end.View(), "end") {
		t.Error("at the end the view should say so")
	}

	// g returns to the newest.
	if back := press(end, "g"); back.offset != 0 {
		t.Errorf("g left offset %d, want 0", back.offset)
	}

	// A page is a full window; paging down then up returns to the top.
	paged := pressKey(m, tea.KeyPgDown)
	if paged.offset != m.activityRowBudget() {
		t.Errorf("pgdown moved %d rows, want %d", paged.offset, m.activityRowBudget())
	}
	if back := pressKey(paged, tea.KeyPgUp); back.offset != 0 {
		t.Errorf("pgup from one page down left offset %d, want 0", back.offset)
	}
}

// TestShrinkingTheWindowClampsTheOffset: a resize must not leave the list
// scrolled past its own end.
func TestShrinkingTheWindowClampsTheOffset(t *testing.T) {
	m := press(busyModel(ViewActivity), "G")
	tall := m.offset

	resized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 60})
	rm := resized.(Model)
	if rm.offset > max(0, len(rm.filteredActivity())-rm.activityRowBudget()) {
		t.Errorf("offset %d exceeds the limit after growing the window", rm.offset)
	}
	if rm.offset >= tall {
		t.Errorf("a taller window should need less scrolling: %d -> %d", tall, rm.offset)
	}
}

// TestFilterCyclesAndReports: `f` walks the directions, the heading names the
// active filter and the counts, and `c` clears it.
func TestFilterCyclesAndReports(t *testing.T) {
	m := busyModel(ViewActivity)
	total := len(m.snap.Status.Activity)

	want := []struct {
		filter string
		label  string
	}{
		{"local", "local→drive"},
		{"remote", "drive→local"},
		{"conflict", "conflict"},
		{"error", "errors"},
		{"", ""},
	}
	for _, c := range want {
		m = press(m, "f")
		if m.dirFilter != c.filter {
			t.Fatalf("f moved to filter %q, want %q", m.dirFilter, c.filter)
		}
		shown := m.filteredActivity()
		if c.filter == "" {
			if len(shown) != total {
				t.Errorf("cleared filter shows %d of %d", len(shown), total)
			}
			continue
		}
		if len(shown) == 0 || len(shown) == total {
			t.Errorf("filter %q selected %d of %d rows — it filtered nothing", c.filter, len(shown), total)
		}
		for _, a := range shown {
			if c.filter == "error" {
				if a.Kind != "error" {
					t.Errorf("error filter kept a %q row", a.Kind)
				}
			} else if a.Source != c.filter {
				t.Errorf("filter %q kept a row with source %q", c.filter, a.Source)
			}
		}
		view := m.View()
		if !strings.Contains(view, c.label) {
			t.Errorf("heading does not name the %q filter: %q", c.filter, firstLines(view, 4))
		}
		if !strings.Contains(view, fmt.Sprintf("%d of %d", len(shown), total)) {
			t.Errorf("heading does not report the counts for %q: %q", c.filter, firstLines(view, 4))
		}
	}

	// c clears both filter and query.
	m = press(press(m, "f"), "c")
	if m.dirFilter != "" || m.query != "" {
		t.Errorf("c left filter %q query %q", m.dirFilter, m.query)
	}
}

// TestSearchIsIncrementalAndEscapable: `/` opens a line that swallows keys —
// including q — enter keeps the query, esc abandons it.
func TestSearchIsIncrementalAndEscapable(t *testing.T) {
	m := press(busyModel(ViewActivity), "/")
	if !m.searching {
		t.Fatal("/ did not open the search line")
	}
	if !strings.Contains(m.View(), "esc cancel") {
		t.Error("the search line should say how to leave it")
	}

	// Typing "q" must search, not quit.
	typed, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = typed.(Model)
	if cmd != nil {
		t.Error("`q` inside a search quit the dashboard")
	}
	if m.query != "q" {
		t.Errorf("query = %q, want q", m.query)
	}

	// Refine to something that matches exactly one row's detail.
	for _, r := range "uarantine" { // -> "quarantine": matches nothing here
		m = press(m, string(r))
	}
	if len(m.filteredActivity()) != 0 {
		t.Errorf("query %q matched %d rows, want 0", m.query, len(m.filteredActivity()))
	}
	if !strings.Contains(m.View(), "no entry matches") {
		t.Error("an empty result must say so, and how to clear it")
	}

	// Backspace edits.
	trimmed := pressKey(m, tea.KeyBackspace)
	if trimmed.query != "quarantin" {
		t.Errorf("backspace left %q", trimmed.query)
	}

	// Esc abandons the query entirely.
	cancelled := pressKey(m, tea.KeyEsc)
	if cancelled.searching || cancelled.query != "" {
		t.Errorf("esc left searching=%v query=%q", cancelled.searching, cancelled.query)
	}

	// Enter keeps it, and the filter still applies with the line closed.
	kept := pressKey(press(press(busyModel(ViewActivity), "/"), "note"), tea.KeyEnter)
	if kept.searching {
		t.Error("enter should close the search line")
	}
	if kept.query != "note" {
		t.Errorf("enter dropped the query: %q", kept.query)
	}
	shown := kept.filteredActivity()
	if len(shown) == 0 {
		t.Fatal(`query "note" matched nothing`)
	}
	for _, a := range shown {
		if !strings.Contains(strings.ToLower(a.RelPath), "note") {
			t.Errorf("query kept a non-matching row: %q", a.RelPath)
		}
	}
	// Ctrl-C still quits from inside a search: it is the universal escape.
	if _, cmd := kept.Update(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil {
		t.Error("ctrl+c inside a search must still quit")
	}
}

// TestSearchMatchesDetailAndKind, not just the path: an error row has no path at
// all, and its text is the only way to find it.
func TestSearchMatchesDetailAndKind(t *testing.T) {
	m := press(busyModel(ViewActivity), "/")
	for _, r := range "attempt" {
		m = press(m, string(r))
	}
	shown := m.filteredActivity()
	if len(shown) == 0 {
		t.Fatal("searching a detail string matched nothing")
	}
	for _, a := range shown {
		if !strings.Contains(a.Detail, "attempt") {
			t.Errorf("kept a row whose detail does not match: %+v", a)
		}
	}

	byKind := press(busyModel(ViewActivity), "/")
	for _, r := range "conflict" {
		byKind = press(byKind, string(r))
	}
	if len(byKind.filteredActivity()) == 0 {
		t.Error("searching by kind matched nothing")
	}
}

// TestFilterAndSearchCombine: they intersect rather than replace each other.
func TestFilterAndSearchCombine(t *testing.T) {
	m := busyModel(ViewActivity)
	m = press(m, "f") // local→drive only
	withFilter := len(m.filteredActivity())

	// "img_880" matches only the first three local rows (8800/8804/8808), so the
	// intersection is strictly smaller than the filter alone.
	m = press(m, "/")
	for _, r := range "img_880" {
		m = press(m, string(r))
	}
	both := m.filteredActivity()
	if len(both) == 0 || len(both) >= withFilter {
		t.Errorf("filter+search kept %d rows, filter alone %d — they should intersect", len(both), withFilter)
	}
	for _, a := range both {
		if a.Source != "local" || !strings.Contains(a.RelPath, "img_880") {
			t.Errorf("row satisfies only one of the two conditions: %+v", a)
		}
	}
	if v := m.View(); !strings.Contains(v, "local→drive") || !strings.Contains(v, "img_880") {
		t.Errorf("heading should name both conditions: %q", firstLines(v, 5))
	}
}

// TestSwitchingViewResetsScroll so a stale offset never greets a returning user.
func TestSwitchingViewResetsScroll(t *testing.T) {
	m := press(busyModel(ViewActivity), "G")
	if m.offset == 0 {
		t.Fatal("fixture should be scrollable")
	}
	overview := press(m, "1")
	if overview.offset != 0 {
		t.Errorf("switching view left offset %d", overview.offset)
	}
}

// TestFilterKeysJumpToTheActivityView: pressing `f` or `/` from the overview
// takes you where the result is visible, rather than filtering invisibly.
func TestFilterKeysJumpToTheActivityView(t *testing.T) {
	for _, key := range []string{"f", "/"} {
		m := press(busyModel(ViewOverview), key)
		if m.view != ViewActivity {
			t.Errorf("key %q left the view at %s", key, ViewName(m.view))
		}
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// TestCountdownAdvancesBetweenDatabaseReads is why the frame ticker exists: at a
// long --interval the snapshot is only re-read occasionally, so relative times
// must be measured from the render instant, not from when the row was gathered.
// Without the interpolated clock the countdown freezes for a whole interval.
func TestCountdownAdvancesBetweenDatabaseReads(t *testing.T) {
	ref := testRef()
	snap := testSnapshot(ref)
	// The daemon says the next poll is 125s after the snapshot instant.
	reads := 0
	now := ref

	m := New(Options{
		Snapshot: snap,
		Width:    100, Height: 30,
		Interval: time.Minute, // deliberately long: one read, many frames
		Clock:    func() time.Time { return now },
		Refresh: func() Snapshot {
			reads++
			return snap // the same stale snapshot, as a slow daemon would give
		},
	})

	first := m.View()
	if !strings.Contains(first, "≈ in 2m") {
		t.Fatalf("expected the initial estimate, got:\n%s", firstLines(first, 6))
	}

	// 70 seconds pass with no new database read.
	now = ref.Add(70 * time.Second)
	later := m.View()
	if later == first {
		t.Error("the frame did not change as time passed — the countdown is frozen")
	}
	if !strings.Contains(later, "≈ in 55s") {
		t.Errorf("countdown did not advance to 55s:\n%s", firstLines(later, 6))
	}
	// The "last sync" column ages too, from 2m to 3m.
	if !strings.Contains(later, "3m ago") {
		t.Errorf("relative times did not age:\n%s", firstLines(later, 8))
	}
	if reads != 0 {
		t.Errorf("rendering hit the database %d times; frames must not read", reads)
	}

	// Past the estimate it reads "now" rather than going negative.
	now = ref.Add(200 * time.Second)
	if got := m.View(); !strings.Contains(got, "≈ now") {
		t.Errorf("an elapsed estimate should read 'now':\n%s", firstLines(got, 6))
	}
}

// TestFrameTickDoesNotReadTheDatabase pins the split: the data ticker reads, the
// frame ticker only redraws.
func TestFrameTickDoesNotReadTheDatabase(t *testing.T) {
	reads := 0
	m := New(Options{
		Snapshot: testSnapshot(testRef()),
		Clock:    testRef,
		Interval: time.Minute,
		Refresh:  func() Snapshot { reads++; return testSnapshot(testRef()) },
	})

	framed, cmd := m.Update(frameMsg(time.Now()))
	if reads != 0 {
		t.Errorf("a frame tick performed %d database reads, want 0", reads)
	}
	if cmd == nil {
		t.Error("a frame tick must schedule the next frame")
	}
	if _, cmd := framed.(Model).Update(tickMsg(time.Now())); cmd == nil {
		t.Error("a data tick must schedule the next data tick")
	}
	if reads != 1 {
		t.Errorf("the data tick performed %d reads, want 1", reads)
	}
}
