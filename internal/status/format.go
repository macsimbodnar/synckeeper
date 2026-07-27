package status

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/service"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// The display formatters shared by every read-only view. They take `now`
// explicitly rather than reading the clock: one instant per rendered frame
// keeps a view internally consistent, and it makes every one of them testable
// without a clock (W15-U1).

// Ago renders a past unix time as a short relative string ("14s ago").
func Ago(now time.Time, unixSecs int64) string {
	if unixSecs <= 0 {
		return "never"
	}
	return Dur(now.Sub(time.Unix(unixSecs, 0))) + " ago"
}

// Until renders a future unix time as "in 31s"; "now" if not in the future.
func Until(now time.Time, unixSecs int64) string {
	if unixSecs <= 0 {
		return "unknown"
	}
	d := time.Unix(unixSecs, 0).Sub(now)
	if d <= 0 {
		return "now"
	}
	return "in " + Dur(d)
}

// Dur renders a duration coarsely: seconds, minutes, hours, or days.
func Dur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

// DirectionLabel renders an activity's change direction for humans.
func DirectionLabel(source string) string {
	switch source {
	case "local":
		return "local→drive"
	case "remote":
		return "drive→local"
	case "conflict":
		return "conflict"
	default:
		return ""
	}
}

// SystemBinLine says where a deletion arriving from Drive lands on this
// machine (W14-M2). Shared by `status`, `doctor` and the dashboard: an absent
// bin is a standing condition — it is the one case where a mass deletion still
// waits for confirmation — so every place states it in the same words.
func SystemBinLine(available bool, dest string) string {
	if available {
		return dest
	}
	return "UNAVAILABLE (" + dest + ") — deletions from Drive are rescued to the quarantine, and a mass deletion asks for --confirm-deletes"
}

// CycleSuffix renders the last cycle summary as " — 3 actions, 3 ok, 0 failed".
func CycleSuffix(cycleJSON string) string {
	cs, ok := ParseCycle(cycleJSON)
	if !ok {
		return ""
	}
	return fmt.Sprintf(" — %d actions, %d ok, %d failed, %dms", cs.Actions, cs.Executed, cs.Failed, cs.DurationMS)
}

// ParseCycle decodes DaemonStatus.LastCycleJSON; ok is false when it is empty
// or unreadable, which a view renders as "no cycle recorded" rather than zeros.
func ParseCycle(cycleJSON string) (statedb.CycleSummary, bool) {
	if cycleJSON == "" {
		return statedb.CycleSummary{}, false
	}
	var cs statedb.CycleSummary
	if err := json.Unmarshal([]byte(cycleJSON), &cs); err != nil {
		return statedb.CycleSummary{}, false
	}
	return cs, true
}

// AutostartText describes the login-service state.
func AutostartText(s service.State, err error) string {
	if err != nil {
		return "unknown (" + err.Error() + ")"
	}
	if !s.Installed {
		return "not installed (run `synckeeper service install`)"
	}
	text := "installed"
	if s.Enabled {
		text += ", starts at login"
	}
	if s.Running {
		text += ", running"
	}
	return text
}
