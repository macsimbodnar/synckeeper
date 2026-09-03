package watch

// The inotify watch budget (S001-L4a).
//
// On Linux fsnotify is inotify, and a watch registration that fails there
// almost always failed with ENOSPC against `fs.inotify.max_user_watches` — a
// *per-user* budget shared with every other application on the machine, not
// this process's file-descriptor limit, which is what the failure latch's
// wording assumed. The behaviour was already right (spec §10: degrade to
// polling, keep syncing, retry and restore automatically); what was missing was
// the cause, so `status` said "polling" and nothing said why.
//
// Everything here compiles on every platform and is pure apart from the one
// injected reader: the value lives in /proc, which exists on Linux alone, so
// reading it is a parameter and building the message is a function of the
// number — both testable on darwin.

import (
	"errors"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

const (
	// inotifyWatchesSysctl is the knob, named verbatim in the log line and in
	// MANUAL §7 so the two cannot drift apart.
	inotifyWatchesSysctl = "fs.inotify.max_user_watches"
	// inotifyWatchesProc holds its current value. Absent off Linux.
	inotifyWatchesProc = "/proc/sys/fs/inotify/max_user_watches"
	// inotifyWatchesAdvised is the raise Dropbox's own support advises for a
	// large tree, and the floor we suggest (decisions.md 2026-07-25).
	inotifyWatchesAdvised = 100000
)

// isWatchLimit reports whether a failed watch registration ran out of kernel
// watch slots rather than of descriptors. ENOSPC is what inotify_add_watch(2)
// returns once max_user_watches is exhausted. syscall.ENOSPC is defined on
// darwin and windows too, so this compiles everywhere and simply never matches
// there — no build tags, no per-platform file.
func isWatchLimit(err error) bool { return errors.Is(err, syscall.ENOSPC) }

// readWatchLimit parses the machine's current watch budget. read is injected
// (os.ReadFile in production) so the parsing is unit-testable off Linux;
// ok is false whenever the file is missing, unreadable, or not a positive
// number — every one of which means "say nothing about the value".
func readWatchLimit(read func(string) ([]byte, error)) (int, bool) {
	raw, err := read(inotifyWatchesProc)
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// watchLimitAdvice builds the one-shot explanation for a latch that tripped on
// exhausted watch slots: the sysctl by name, its current value, the raise, and
// what Synckeeper is doing meanwhile. Empty off Linux — ENOSPC there would mean
// something else entirely, and a wrong cause is worse than no cause.
//
// The suggested value is the advised floor, or double the current limit when
// the machine is already above it: telling someone with 500000 watches to set
// 100000 would be advice to make things worse.
func watchLimitAdvice(goos string, limit int, known bool) string {
	if goos != "linux" {
		return ""
	}
	current := "unknown"
	target := inotifyWatchesAdvised
	if known {
		current = strconv.Itoa(limit)
		if limit >= target {
			target = limit * 2
		}
	}
	return "out of inotify watches: " + inotifyWatchesSysctl + " is " + current +
		" on this machine, and that budget is per user — shared with every other application. " +
		"Raise it with `sudo sysctl -w " + inotifyWatchesSysctl + "=" + strconv.Itoa(target) +
		"` (persist it in /etc/sysctl.d/). Syncing continues by polling in the meantime and " +
		"file watching is restored automatically once watches are free."
}

// watchLimitHint is the production entry point: read the live value, build the
// line. Empty when there is nothing truthful to say. A var so a test can put a
// Linux answer in front of it on a machine that has no /proc, the same seam
// style as newBackend and newNotifyWatcher.
var watchLimitHint = liveWatchLimitHint

func liveWatchLimitHint() string {
	limit, ok := readWatchLimit(os.ReadFile)
	return watchLimitAdvice(runtime.GOOS, limit, ok)
}
