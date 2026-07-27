package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Colours are applied only when the caller enables them (a real terminal with
// NO_COLOR unset). Layout — padding, joins, rules — is colour-independent, so a
// golden test asserts the exact frame a user sees minus the escape codes.
var (
	colGood    = lipgloss.Color("42")  // green: running, healthy
	colWarn    = lipgloss.Color("214") // amber: paused, degraded
	colBad     = lipgloss.Color("203") // red: stopped, error, guard
	colMuted   = lipgloss.Color("245") // grey: labels, chrome
	colAccent  = lipgloss.Color("39")  // blue: headings, active view
	colLocal   = lipgloss.Color("39")  // local→drive
	colRemote  = lipgloss.Color("42")  // drive→local
	colConflct = lipgloss.Color("214") // conflict
)

// theme paints strings, or does not. A zero theme (color=false) returns every
// string unchanged, which is what tests and NO_COLOR use.
type theme struct{ color bool }

func (t theme) paint(c lipgloss.Color, s string) string {
	if !t.color {
		return s
	}
	return lipgloss.NewStyle().Foreground(c).Render(s)
}

func (t theme) bold(s string) string {
	if !t.color {
		return s
	}
	return lipgloss.NewStyle().Bold(true).Render(s)
}

func (t theme) good(s string) string   { return t.paint(colGood, s) }
func (t theme) warn(s string) string   { return t.paint(colWarn, s) }
func (t theme) bad(s string) string    { return t.paint(colBad, s) }
func (t theme) muted(s string) string  { return t.paint(colMuted, s) }
func (t theme) accent(s string) string { return t.paint(colAccent, s) }

// rule renders a section heading as `── label ──────────`, the full width.
func (t theme) rule(label string, width int) string {
	if width < 8 {
		return label
	}
	head := "── " + label + " "
	if n := width - visibleLen(head); n > 0 {
		head += strings.Repeat("─", n)
	}
	return t.muted(head)
}

// visibleLen counts runes, which is what matters for our own composed layout:
// nothing here contains escape codes before the theme paints it, and paint is
// always the last step applied to a cell.
func visibleLen(s string) int { return len([]rune(s)) }

// truncate shortens s to width runes, marking the cut with an ellipsis. Paths
// are cut from the left (the tail — the file name — is the informative end);
// everything else from the right.
func truncate(s string, width int) string {
	r := []rune(s)
	if width <= 0 || len(r) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return string(r[:width-1]) + "…"
}

func truncatePath(s string, width int) string {
	r := []rune(s)
	if width <= 0 || len(r) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return "…" + string(r[len(r)-(width-1):])
}

// pad right-pads to width runes (no-op when already wider).
func pad(s string, width int) string {
	if n := width - visibleLen(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// comma groups an integer with thousands separators: 12481 → "12,481".
func comma(n int) string {
	s := itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var out []byte
	for i, d := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, d)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
