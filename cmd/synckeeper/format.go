package main

import (
	"fmt"
	"time"
)

// ago renders a past unix time as a short relative string ("14s ago").
func ago(unixSecs int64) string {
	if unixSecs <= 0 {
		return "never"
	}
	return dur(time.Since(time.Unix(unixSecs, 0))) + " ago"
}

// until renders a future unix time as "in 31s"; "now" if not in the future.
func until(unixSecs int64) string {
	if unixSecs <= 0 {
		return "unknown"
	}
	d := time.Until(time.Unix(unixSecs, 0))
	if d <= 0 {
		return "now"
	}
	return "in " + dur(d)
}

// dur renders a duration coarsely: seconds, minutes, hours, or days.
func dur(d time.Duration) string {
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
