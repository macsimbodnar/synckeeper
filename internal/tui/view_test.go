package tui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/macsimbodnar/synckeeper/internal/service"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
	"github.com/macsimbodnar/synckeeper/internal/status"
)

// The goldens here are a regression net, not a characterization net: unlike
// W15-U1's (captured from the pre-dashboard renderer), these frames are new, so
// they pin the layout against accidental change rather than proving nothing
// moved. Colour is off in every case, so a golden is the exact frame minus
// escape codes — layout bugs show up as diffs instead of hiding in ANSI.
//
//	go test ./internal/tui/ -update
var update = flag.Bool("update", false, "rewrite the view goldens")

// testRef is a fixed instant in UTC, not time.Now(): the header shows a wall
// clock, and Snapshot.Now is the only clock a frame reads, so pinning it makes
// every golden byte-stable — in any timezone, on any machine. Offsets are still
// >= 61s and off a minute boundary because status.Dur truncates with int()
// (W15-U1).
func testRef() time.Time { return time.Date(2026, 7, 27, 14, 45, 2, 0, time.UTC) }
func testSnapshot(ref time.Time) Snapshot {
	return Snapshot{
		Status: status.Snapshot{
			Now:          ref,
			State:        status.StateRunning,
			ConfigDir:    "/home/max/.config/synckeeper",
			SyncDir:      "/home/max/Synckeeper",
			DriveFolder:  "Synckeeper",
			MachineName:  "max_mbp",
			RootID:       "root123",
			TokenOK:      true,
			Items:        12481,
			Pending:      0,
			BinAvailable: true,
			BinDest:      "macOS Trash (Finder)",
			Autostart:    service.State{Installed: true, Enabled: true, Running: true},
			Daemon: statedb.DaemonStatus{
				Running:         true,
				PID:             4242,
				StartedAt:       ref.Add(-(3*24*time.Hour + 4*time.Hour + 30*time.Minute)).Unix(),
				LastHeartbeatAt: ref.Add(-65 * time.Second).Unix(),
				Mode:            "watching",
				LastSyncAt:      ref.Add(-125 * time.Second).Unix(),
				LastCycleJSON:   `{"actions":3,"executed":3,"failed":0,"duration_ms":412}`,
				NextPollAt:      ref.Add(125 * time.Second).Unix(),
			},
			Activity: []statedb.Activity{
				{TS: ref.Add(-125 * time.Second).Unix(), Kind: "upload", RelPath: "Photos/2026/img_8841.raf", Source: "local"},
				{TS: ref.Add(-125 * time.Second).Unix(), Kind: "download", RelPath: "Notes/todo.md", Source: "remote"},
				{TS: ref.Add(-185 * time.Second).Unix(), Kind: "conflict", RelPath: "Budget.xlsx", Detail: "-> Budget (conflict max_mbp).xlsx", Source: "conflict"},
				{TS: ref.Add(-245 * time.Second).Unix(), Kind: "trash", RelPath: "Archive/old", Detail: "(1117 files)", Source: "remote"},
			},
		},
		Info: []InfoRow{
			{Label: "version", Value: "synckeeper 1.2.3"},
			{},
			{Label: "config dir", Value: "/home/max/.config/synckeeper"},
			{Label: "token.json", Value: "/home/max/.config/synckeeper/token.json", Note: "present, 0600"},
			{Label: "credentials", Value: "/home/max/.config/synckeeper/credentials.json", Note: "present, 0664 — readable by others; chmod 600"},
			{Label: "system bin", Value: "macOS Trash (Finder)"},
			{},
			{Label: "sync dir", Value: "/home/max/Synckeeper"},
			{Label: "machine", Value: "max_mbp", Note: "id ab12cd"},
			{Label: "poll interval", Value: "45s"},
			{Label: "ignore", Value: "*.tmp .DS_Store"},
		},
	}
}

func frames(ref time.Time) map[string]Model {
	base := Options{Snapshot: testSnapshot(ref), Width: 100, Height: 30, Clock: testRef}

	overview := New(base)

	activity := New(base)
	activity.view = ViewActivity

	info := New(base)
	info.view = ViewInfo

	help := New(base)
	help.showHelp = true

	narrow := Options{Snapshot: testSnapshot(ref), Width: 60, Height: 24, Clock: testRef}

	guardOpts := base
	gs := testSnapshot(ref)
	gs.Status.Daemon.GuardBlocked = true
	gs.Status.Daemon.GuardReason = "plan deletes 1117 of 1118 tracked files (over the 25% mass-delete threshold)"
	gs.Status.BinAvailable = false
	gs.Status.BinDest = "no trash implementation for this platform"
	gs.Status.QFiles, gs.Status.QBytes = 3, 12345
	guardOpts.Snapshot = gs

	stale := base
	ss := testSnapshot(ref)
	ss.Status.State = status.StateStale
	ss.Status.Daemon.LastError = "refresh remote state: dial tcp: lookup www.googleapis.com: no such host"
	ss.Status.Autostart = service.State{}
	stale.Snapshot = ss

	never := base
	ns := testSnapshot(ref)
	ns.Status.State = status.StateNeverRun
	ns.Status.Daemon = statedb.DaemonStatus{}
	ns.Status.TokenOK = false
	ns.Status.Items = 0
	ns.Status.Autostart = service.State{}
	ns.Status.Activity = nil
	never.Snapshot = ns

	paused := base
	ps := testSnapshot(ref)
	ps.Status.Daemon.Paused = true
	ps.Status.Daemon.Mode = "paused"
	paused.Snapshot = ps

	busy := New(Options{Snapshot: busySnapshot(ref), Width: 100, Height: 20, Clock: testRef})
	busy.view = ViewActivity

	scrolled := busy
	scrolled.offset = 12

	filtered := busy
	filtered.dirFilter = "error"

	searching := busy
	searching.searching = true
	searching.query = "note"

	return map[string]Model{
		"activity_scrolled": scrolled,
		"activity_filtered": filtered,
		"activity_search":   searching,
		"overview":          overview,
		"activity":          activity,
		"info":              info,
		"help":              help,
		"overview_narrow":   New(narrow),
		"overview_guard":    New(guardOpts),
		"overview_stale":    New(stale),
		"overview_neverrun": New(never),
		"overview_paused":   New(paused),
	}
}

func TestViewGoldens(t *testing.T) {
	ref := testRef()
	for name, m := range frames(ref) {
		t.Run(name, func(t *testing.T) {
			got := m.View()
			path := filepath.Join("testdata", name+".golden")
			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				t.Skip("golden rewritten")
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("missing golden (run: go test ./internal/tui/ -update): %v", err)
			}
			if got != string(want) {
				t.Errorf("frame changed.\n--- want ---\n%s\n--- got ---\n%s", want, got)
			}
		})
	}
}

// TestNoEscapeCodesWithColorOff is what makes the goldens trustworthy: with
// colour off the frame must be plain text, so a diff is a layout change and
// never a palette change.
func TestNoEscapeCodesWithColorOff(t *testing.T) {
	for name, m := range frames(testRef()) {
		if strings.Contains(m.View(), "\x1b[") {
			t.Errorf("%s: frame contains escape codes with color disabled", name)
		}
	}
}

// TestNarrowLayoutStacksPanels pins the degradation rule: below narrowWidth the
// two-column overview becomes one column, because side-by-side truncates both
// halves into uselessness.
func TestNarrowLayoutStacksPanels(t *testing.T) {
	ref := testRef()
	wide := New(Options{Snapshot: testSnapshot(ref), Width: 100, Height: 30, Clock: testRef}).View()
	narrow := New(Options{Snapshot: testSnapshot(ref), Width: 60, Height: 24, Clock: testRef}).View()

	// Wide: the cycle and totals headings share a line. Narrow: they do not.
	wideShared, narrowShared := false, false
	for _, line := range strings.Split(wide, "\n") {
		if strings.Contains(line, "cycle") && strings.Contains(line, "totals") {
			wideShared = true
		}
	}
	for _, line := range strings.Split(narrow, "\n") {
		if strings.Contains(line, "cycle") && strings.Contains(line, "totals") {
			narrowShared = true
		}
	}
	if !wideShared {
		t.Error("at 100 columns the cycle and totals panels should sit side by side")
	}
	if narrowShared {
		t.Error("at 60 columns the panels should stack, not share a line")
	}
	for _, line := range strings.Split(narrow, "\n") {
		if got := len([]rune(strings.TrimRight(line, " "))); got > 60 {
			t.Errorf("narrow frame line overflows 60 columns (%d): %q", got, line)
		}
	}
}

// TestFrameNeverOverflowsWidth guards every view, including a tiny window.
func TestFrameNeverOverflowsWidth(t *testing.T) {
	ref := testRef()
	for _, w := range []int{40, 60, 80, 100, 140} {
		for v := ViewOverview; v < numViews; v++ {
			m := New(Options{Snapshot: testSnapshot(ref), Width: w, Height: 20, Clock: testRef})
			m.view = v
			for _, line := range strings.Split(m.View(), "\n") {
				if got := len([]rune(strings.TrimRight(line, " "))); got > w {
					t.Errorf("view %s at width %d: line of %d runes: %q", ViewName(v), w, got, line)
				}
			}
		}
	}
}

// TestKeysNavigateAndQuit covers the whole key surface U2 ships.
func TestKeysNavigateAndQuit(t *testing.T) {
	ref := testRef()
	start := New(Options{Snapshot: testSnapshot(ref), Width: 100, Height: 30, Clock: testRef})

	press := func(m Model, key string) (Model, tea.Cmd) {
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		return next.(Model), cmd
	}

	cases := []struct {
		key  string
		want int
	}{{"2", ViewActivity}, {"3", ViewInfo}, {"1", ViewOverview}}
	for _, c := range cases {
		m, _ := press(start, c.key)
		if m.view != c.want {
			t.Errorf("key %q selected view %d, want %d", c.key, m.view, c.want)
		}
	}

	// Tab cycles forward and wraps.
	m := start
	for i := 1; i <= numViews; i++ {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
		m = next.(Model)
		if want := i % numViews; m.view != want {
			t.Errorf("after %d tabs view = %d, want %d", i, m.view, want)
		}
	}

	// ? toggles help, and selecting a view closes it.
	h, _ := press(start, "?")
	if !h.showHelp {
		t.Error("? did not open help")
	}
	if !strings.Contains(h.View(), "toggle this help") {
		t.Error("help frame does not list its own key")
	}
	back, _ := press(h, "2")
	if back.showHelp {
		t.Error("selecting a view should close help")
	}

	// q and ctrl+c quit.
	for _, msg := range []tea.Msg{
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")},
		tea.KeyMsg{Type: tea.KeyCtrlC},
	} {
		if _, cmd := start.Update(msg); cmd == nil {
			t.Errorf("%v did not quit", msg)
		}
	}
}

// TestResizeAndTickAreHandled: a resize must be adopted, and a tick must pull a
// fresh snapshot and schedule the next one.
func TestResizeAndTickAreHandled(t *testing.T) {
	ref := testRef()
	calls := 0
	m := New(Options{
		Snapshot: testSnapshot(ref),
		Width:    80, Height: 24,
		Clock:    testRef,
		Interval: 10 * time.Millisecond,
		Refresh: func() Snapshot {
			calls++
			s := testSnapshot(ref)
			s.Status.Items = 99
			return s
		},
	})

	resized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	rm := resized.(Model)
	if rm.width != 120 || rm.height != 40 {
		t.Errorf("resize not adopted: %dx%d", rm.width, rm.height)
	}

	ticked, cmd := rm.Update(tickMsg(time.Now()))
	tm := ticked.(Model)
	if calls != 1 {
		t.Errorf("tick called refresh %d times, want 1", calls)
	}
	if tm.snap.Status.Items != 99 {
		t.Error("tick did not adopt the refreshed snapshot")
	}
	if cmd == nil {
		t.Error("tick did not schedule the next tick")
	}

	// `r` refreshes immediately.
	pressed, _ := tm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	_ = pressed
	if calls != 2 {
		t.Errorf("r called refresh %d times total, want 2", calls)
	}

	// A model with no refresher must not schedule ticks or panic on one.
	still := New(Options{Snapshot: testSnapshot(ref), Clock: testRef})
	if still.Init() != nil {
		t.Error("a model without Refresh should not start a ticker")
	}
	if _, cmd := still.Update(tickMsg(time.Now())); cmd != nil {
		t.Error("a model without Refresh should not reschedule on a tick")
	}
}

// TestAttentionOnlyWhenSomethingIsWrong: the panel must be absent on a healthy
// machine, and must name both the problem and the way out when there is one.
func TestAttentionOnlyWhenSomethingIsWrong(t *testing.T) {
	ref := testRef()
	healthy := New(Options{Snapshot: testSnapshot(ref), Width: 100, Height: 30, Clock: testRef}).View()
	if strings.Contains(healthy, "attention") {
		t.Error("a healthy machine should draw no attention panel")
	}

	s := testSnapshot(ref)
	s.Status.Daemon.GuardBlocked = true
	s.Status.Daemon.GuardReason = "plan deletes 1117 of 1118 tracked files"
	s.Status.BinAvailable = false
	s.Status.BinDest = "no trash implementation for this platform"
	s.Status.TokenOK = false
	s.Status.Autostart = service.State{}
	got := New(Options{Snapshot: s, Width: 100, Height: 40, Clock: testRef}).View()

	for _, want := range []string{
		"attention",
		"guard BLOCKED",
		"--confirm-deletes", // the way out, not just the diagnosis
		"no system bin",
		"no saved credentials",
		"autostart not installed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("attention panel missing %q:\n%s", want, got)
		}
	}
}

// TestNextPollIsMarkedAsAnEstimate: NextPollAt is recorded at cycle end while
// the ticker runs independently and a local change pre-empts it, so the
// dashboard must not present it as a deadline (W15 finding, decisions.md).
func TestNextPollIsMarkedAsAnEstimate(t *testing.T) {
	got := New(Options{Snapshot: testSnapshot(testRef()), Width: 100, Height: 30, Clock: testRef}).View()
	if !strings.Contains(got, "≈") {
		t.Error("the next-poll figure must be marked as an estimate")
	}
}

func TestComma(t *testing.T) {
	for _, c := range []struct {
		in   int
		want string
	}{{0, "0"}, {7, "7"}, {999, "999"}, {1000, "1,000"}, {12481, "12,481"}, {1234567, "1,234,567"}, {-4321, "-4,321"}} {
		if got := comma(c.in); got != c.want {
			t.Errorf("comma(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTruncation(t *testing.T) {
	if got := truncate("abcdefgh", 4); got != "abc…" {
		t.Errorf("truncate = %q", got)
	}
	// A path keeps its tail: the file name is the informative end.
	if got := truncatePath("/very/long/path/to/file.txt", 12); got != "…o/file.txt" && got != "…to/file.txt" {
		t.Errorf("truncatePath = %q, want the tail kept", got)
	}
	if got := truncate("abc", 10); got != "abc" {
		t.Errorf("truncate should not pad: %q", got)
	}
}
