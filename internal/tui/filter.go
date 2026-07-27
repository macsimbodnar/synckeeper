package tui

import (
	"strings"

	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// filteredActivity applies the direction filter and the search query, newest
// first, without mutating the snapshot. It is a plain function of model state so
// the scroll maths and the rendering agree on what "row 12" means.
func (m Model) filteredActivity() []statedb.Activity {
	acts := m.snap.Status.Activity
	if m.dirFilter == "" && m.query == "" {
		return acts
	}
	out := make([]statedb.Activity, 0, len(acts))
	for _, a := range acts {
		if !matchesDir(a, m.dirFilter) || !matchesQuery(a, m.query) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// matchesDir: "error" selects the rows the recorder writes with no source (a
// failed cycle), which is why it cannot simply compare Source.
func matchesDir(a statedb.Activity, filter string) bool {
	switch filter {
	case "":
		return true
	case "error":
		return a.Kind == "error"
	default:
		return a.Source == filter
	}
}

// matchesQuery is a case-insensitive substring match over everything visible in
// a row: the path, the detail, and the kind. Nothing clever — the ring holds 500
// rows, and a user typing "conflict" or a folder name expects both to work.
func matchesQuery(a statedb.Activity, query string) bool {
	if query == "" {
		return true
	}
	q := strings.ToLower(query)
	return strings.Contains(strings.ToLower(a.RelPath), q) ||
		strings.Contains(strings.ToLower(a.Detail), q) ||
		strings.Contains(strings.ToLower(a.Kind), q)
}

// filterLabel describes the active filters for the view heading; empty when
// everything is showing.
func filterLabel(dirFilter, query string) string {
	var parts []string
	switch dirFilter {
	case "":
	case "error":
		parts = append(parts, "errors")
	case "local":
		parts = append(parts, "local→drive")
	case "remote":
		parts = append(parts, "drive→local")
	default:
		parts = append(parts, dirFilter)
	}
	if query != "" {
		parts = append(parts, "“"+query+"”")
	}
	return strings.Join(parts, " + ")
}
