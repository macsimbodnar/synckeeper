package tui

import (
	"context"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Run drives the dashboard on the alternate screen until the user quits.
//
// refresh is called on every tick, on the main loop, so it must not block for
// long: it reads one status row plus a bounded activity query out of SQLite
// (WAL, so the writing daemon never blocks it).
func Run(ctx context.Context, o Options) error {
	if o.Refresh != nil {
		o.Snapshot = o.Refresh() // draw a full first frame, not an empty one
	}
	opts := []tea.ProgramOption{tea.WithContext(ctx)}
	if o.Output != nil {
		// A test drives the real program through a buffer; the alternate screen
		// would fill it with cursor gymnastics instead of the frame.
		opts = append(opts, tea.WithOutput(o.Output))
	} else {
		opts = append(opts, tea.WithAltScreen())
	}
	if o.Input != nil {
		opts = append(opts, tea.WithInput(o.Input))
	}
	p := tea.NewProgram(New(o), opts...)
	_, err := p.Run()
	if err != nil && ctx.Err() != nil {
		return nil // Ctrl-C / SIGTERM is a clean exit, not a failure
	}
	return err
}

// ColorEnabled reports whether the dashboard should paint. NO_COLOR (any value,
// per no-color.org) and TERM=dumb both turn it off; the caller adds the
// is-a-terminal check.
func ColorEnabled() bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	return os.Getenv("TERM") != "dumb"
}

// DefaultInterval is the refresh cadence: fast enough that a sync cycle is
// visible while it happens, slow enough to stay free next to a daemon that
// polls every 45 s.
const DefaultInterval = time.Second
