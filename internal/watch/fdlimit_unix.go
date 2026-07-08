//go:build !windows

package watch

import (
	"log/slog"
	"syscall"
)

// raiseFDLimit lifts the soft open-file limit to the hard limit. On macOS
// fsnotify's kqueue backend holds one descriptor per watched file, so the
// default soft limit (often 256) starves a watcher on any non-trivial tree.
func raiseFDLimit() {
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		return
	}
	if lim.Cur >= lim.Max {
		return
	}
	want := lim.Max
	lim.Cur = want
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		// Darwin refuses values above kern.maxfilesperproc even when the
		// hard limit reads as unlimited; fall back to the classic OPEN_MAX.
		lim.Cur = 10240
		if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
			slog.Warn("could not raise open-file limit; large trees may exhaust fsnotify watches", "err", err)
			return
		}
		want = lim.Cur
	}
	slog.Debug("raised open-file limit", "limit", want)
}
