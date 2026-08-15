package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/statedb"
	"github.com/macsimbodnar/synckeeper/internal/status"
)

// driveAuthError is the error a revoked refresh token produces, verbatim from
// the field (2026-08-15): five lines, of which only the first is information.
const driveAuthError = "Get \"https://www.googleapis.com/drive/v3/changes?pageToken=204212&prettyPrint=false\": " +
	"auth: cannot fetch token: 400\nResponse: {\n  \"error\": \"invalid_grant\",\n  " +
	"\"error_description\": \"Token has been expired or revoked.\"\n}"

func frameLines(s string) int { return len(strings.Split(strings.TrimSuffix(s, "\n"), "\n")) }

// countActivityRows counts rendered activity rows by their timestamp column,
// which only a row start carries — so it counts rows, not lines of one row.
var rowStart = regexp.MustCompile(`(?m)^\s+\d+[smhd][0-9a-z]* ago\s`)

func countActivityRows(frame string) int { return len(rowStart.FindAllString(frame, -1)) }

// TestFrameNeverExceedsTheTerminalHeight is the invariant the field bug broke:
// every panel budgets in rows, so one value that renders as five lines pushes
// the header, the panels and the attention block off the top of the alternate
// screen — which is what the user saw, a whole screen of nothing but error
// text. Asserted for every golden state, not just the error one.
func TestFrameNeverExceedsTheTerminalHeight(t *testing.T) {
	ref := testRef()
	for name, m := range frames(ref) {
		t.Run(name, func(t *testing.T) {
			if got := frameLines(m.View()); got > m.height {
				t.Errorf("frame is %d lines in a %d-line terminal", got, m.height)
			}
		})
	}
}

// multilineErrorModel is a dashboard whose whole activity ring is the field's
// five-line auth error, with the same text as the daemon's last error.
func multilineErrorModel(ref time.Time, view int) Model {
	snap := testSnapshot(ref)
	snap.Status.Daemon.LastError = driveAuthError
	snap.Status.Activity = nil
	for i := 0; i < 60; i++ {
		detail := driveAuthError
		// Every third row is a *short* multi-line error. It matters: a long one
		// loses its newlines to the width cut anyway, so only a message that
		// fits the column proves the cell itself is flattened.
		if i%3 == 0 {
			detail = "upload failed:\nquota exceeded"
		}
		snap.Status.Activity = append(snap.Status.Activity, statedb.Activity{
			TS: ref.Add(-time.Duration(i) * time.Minute).Unix(), Kind: "error", Detail: detail,
		})
	}
	m := New(Options{Snapshot: snap, Width: 100, Height: 30, Clock: testRef})
	m.view = view
	return m
}

// TestAMultilineErrorStaysOneRow is the regression itself: an activity detail
// and a last error that arrive with newlines are rendered as one row each, so
// the frame still fits and the header — the line that answers "is it working?"
// — is still on screen.
func TestAMultilineErrorStaysOneRow(t *testing.T) {
	ref := testRef()
	for _, view := range []int{ViewOverview, ViewActivity} {
		m := multilineErrorModel(ref, view)
		out := m.View()
		if got := frameLines(out); got > m.height {
			t.Errorf("view %d: frame is %d lines in a %d-line terminal", view, got, m.height)
		}
		first := strings.SplitN(out, "\n", 2)[0]
		if !strings.Contains(first, "synckeeper") {
			t.Errorf("view %d: the header is not the first line: %q", view, first)
		}
		// The discriminating assertions: a row is one line, so each view still
		// shows what it budgeted for. Unflattened, one row eats five lines, the
		// frame's height guard throws the rest away, and the user gets a screen
		// of error text with nothing else on it — the reported bug.
		if view == ViewActivity {
			if got := countActivityRows(out); got < 15 {
				t.Errorf("activity view: only %d rows fit the frame; a row is not one line:\n%s", got, out)
			}
		} else {
			for _, panel := range []string{"cycle", "totals", "attention", "last error"} {
				if !strings.Contains(out, panel) {
					t.Errorf("overview lost its %q panel to the error rows:\n%s", panel, out)
				}
			}
		}
		if strings.Contains(out, "\"error\": \"invalid_grant\",\n") {
			t.Errorf("view %d: the raw JSON body reached the screen", view)
		}
		// The head of the message survives: a row with no path is cut from the
		// right, so what is kept is the sentence, not the JSON tail.
		if !strings.Contains(out, `Get "https://www.googleapis.com`) {
			t.Errorf("view %d: the error text was lost, not flattened:\n%s", view, out)
		}
		if strings.Contains(out, "expired or revoked.\" }") {
			t.Errorf("view %d: the row kept the tail and cut the message", view)
		}
	}
}

// TestFitBodyIsTheLastResort: even a body that ignores its budget entirely
// cannot scroll the frame, because View clamps what it emits.
func TestFitBodyIsTheLastResort(t *testing.T) {
	m := New(Options{Snapshot: testSnapshot(testRef()), Width: 100, Height: 12, Clock: testRef})
	body := strings.Repeat("x\n", 200)
	clamped := m.fitBody(theme{}, body, false)
	if got := len(strings.Split(clamped, "\n")); got != m.height-chromeRows {
		t.Fatalf("clamped body is %d lines, want %d", got, m.height-chromeRows)
	}
	if !strings.Contains(clamped, "too short") {
		t.Error("the clamp cut the body without saying so")
	}
}

// TestFitBodyPadsAShortBody is the other direction, and the reported bug: a
// body with less to say than the window has rows is padded, so the footer never
// climbs up the screen behind a short panel.
func TestFitBodyPadsAShortBody(t *testing.T) {
	m := New(Options{Snapshot: testSnapshot(testRef()), Width: 100, Height: 40, Clock: testRef})
	for _, hasNotice := range []bool{false, true} {
		got := m.fitBody(theme{}, "one\ntwo", hasNotice)
		want := m.height - chromeRows
		if hasNotice {
			want--
		}
		if n := len(strings.Split(got, "\n")); n != want {
			t.Errorf("notice=%v: padded body is %d lines, want %d", hasNotice, n, want)
		}
		if !strings.HasPrefix(got, "one\ntwo\n") {
			t.Errorf("notice=%v: padding rewrote the body: %q", hasNotice, got)
		}
	}
}

// TestTheFooterIsTheLastLine pins the frame to the full height of the terminal
// in every state: exactly as many lines as rows, with the tab bar on the last
// one. A frame short of the bottom was the field report (2026-08-15) — with
// four tracked files the footer sat a third of the way down a 58-row window.
func TestTheFooterIsTheLastLine(t *testing.T) {
	ref := testRef()
	for name, m := range frames(ref) {
		t.Run(name, func(t *testing.T) {
			lines := strings.Split(m.View(), "\n")
			if len(lines) != m.height {
				t.Fatalf("frame is %d lines in a %d-line terminal", len(lines), m.height)
			}
			// The tab bar, not the hint half: a narrow window drops the hints.
			if last := lines[len(lines)-1]; !strings.Contains(last, "1 overview") {
				t.Errorf("the last line is not the footer: %q", last)
			}
		})
	}
}

// TestOneLineIsSharedByWriterAndReader pins that the dashboard flattens with
// the same function the daemon stores with, so the two cannot drift.
func TestOneLineIsSharedByWriterAndReader(t *testing.T) {
	if got := truncate(driveAuthError, 400); strings.Contains(got, "\n") {
		t.Errorf("truncate left a newline in a cell: %q", got)
	}
	if got := truncatePath("Notes/two\nline.md", 40); got != status.OneLine("Notes/two\nline.md") {
		t.Errorf("truncatePath = %q, want the flattened path", got)
	}
}

// TestTheDashboardNamesARejectedToken: the attention panel exists to tell the
// user what to do without being asked. A revoked refresh token is the one
// failure retrying never clears, and its raw OAuth text says nothing useful.
func TestTheDashboardNamesARejectedToken(t *testing.T) {
	snap := testSnapshot(testRef())
	snap.Status.Daemon.LastError = driveAuthError
	snap.Status.Daemon.LastErrorAuth = true
	snap.Status.TokenOK, snap.Status.TokenRejected = true, true
	m := New(Options{Snapshot: snap, Width: 100, Height: 30, Clock: testRef})

	out := m.View()
	if !strings.Contains(out, "credentials expired") || !strings.Contains(out, "synckeeper login") {
		t.Errorf("the overview does not name the cure:\n%s", out)
	}
	// It leads the attention block: everything else on the screen is a symptom.
	att := strings.Index(out, "credentials expired")
	if last := strings.Index(out, "last error"); last >= 0 && last < att {
		t.Error("the raw error is listed above the credentials line")
	}
}

// TestAFoldedRowShowsItsCount: one row standing for 288 failed cycles must say
// how many, and the count must survive the width cut — which cuts a message
// from the right and a path from the left, so it sits on opposite ends.
func TestAFoldedRowShowsItsCount(t *testing.T) {
	ref := testRef()
	snap := testSnapshot(ref)
	snap.Status.Activity = []statedb.Activity{
		{TS: ref.Add(-time.Minute).Unix(), Kind: "error", Detail: driveAuthError, Count: 288},
		{TS: ref.Add(-2 * time.Minute).Unix(), Kind: "upload", RelPath: "Photos/2026/a-very-long-folder-name/img_8841.raf", Source: "local", Count: 7},
	}
	m := New(Options{Snapshot: snap, Width: 80, Height: 30, Clock: testRef})
	m.view = ViewActivity

	out := m.View()
	if !strings.Contains(out, "×288") {
		t.Errorf("the folded error does not show its count:\n%s", out)
	}
	if !strings.Contains(out, "×7") {
		t.Errorf("the folded upload does not show its count:\n%s", out)
	}
}
