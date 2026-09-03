package watch

// W7-L4 (S001). Two behaviours, both driven through seams so they execute on
// every platform rather than only on Linux hardware:
//
//   L4a — a failure latch that tripped on exhausted kernel watch slots names
//         fs.inotify.max_user_watches, its current value, and the sysctl fix,
//         exactly once and not per failed directory.
//   L4b — a kernel event-queue overflow is a wake, not just a log line: events
//         were dropped, so the loop rescans now instead of waiting out the poll.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/macsimbodnar/synckeeper/internal/engine"
)

// captureWarnings redirects the default logger for the duration of the test and
// returns the accumulated text.
func captureWarnings(t *testing.T) func() string {
	t.Helper()
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return logs.String
}

// nopBackend is an fsWatcher that does nothing; latchIfNeeded only ever closes
// the one it is handed.
type nopBackend struct{ closed atomic.Int32 }

func (n *nopBackend) refresh(string) int { return 0 }
func (n *nopBackend) close() error       { n.closed.Add(1); return nil }
func (n *nopBackend) needsRebuild() bool { return false }
func (n *nopBackend) name() string       { return "nop" }

// L4a: ENOSPC from inotify_add_watch is the per-user watch budget, not this
// process's descriptors, and only the former has a sysctl fix.
func TestIsWatchLimit(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"bare ENOSPC", syscall.ENOSPC, true},
		{"wrapped ENOSPC", fmt.Errorf("inotify_add_watch: %w", syscall.ENOSPC), true},
		{"EMFILE (descriptors, not watches)", syscall.EMFILE, false},
		{"ENOENT", syscall.ENOENT, false},
		{"a plain error", errors.New("too many open files"), false},
		{"nil", nil, false},
	} {
		if got := isWatchLimit(tc.err); got != tc.want {
			t.Errorf("isWatchLimit(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The /proc read is a parameter, so the parsing is proved off Linux too.
func TestReadWatchLimit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		read  func(string) ([]byte, error)
		want  int
		wantK bool
	}{
		{"a normal kernel default", func(string) ([]byte, error) { return []byte("127469\n"), nil }, 127469, true},
		{"no trailing newline", func(string) ([]byte, error) { return []byte("8192"), nil }, 8192, true},
		{"file absent (every non-Linux machine)", func(string) ([]byte, error) { return nil, os.ErrNotExist }, 0, false},
		{"garbage", func(string) ([]byte, error) { return []byte("many"), nil }, 0, false},
		{"empty", func(string) ([]byte, error) { return []byte(""), nil }, 0, false},
		{"zero is not a usable limit", func(string) ([]byte, error) { return []byte("0\n"), nil }, 0, false},
	} {
		got, ok := readWatchLimit(tc.read)
		if got != tc.want || ok != tc.wantK {
			t.Errorf("readWatchLimit(%s) = (%d, %v), want (%d, %v)", tc.name, got, ok, tc.want, tc.wantK)
		}
	}

	// It reads the documented path and nothing else.
	var asked string
	readWatchLimit(func(p string) ([]byte, error) { asked = p; return nil, os.ErrNotExist })
	if asked != "/proc/sys/fs/inotify/max_user_watches" {
		t.Errorf("read %q, want /proc/sys/fs/inotify/max_user_watches", asked)
	}
}

// L4a: the message a user has to act on. The sysctl is named verbatim, the
// current value is stated, and the remedy is a command they can paste.
func TestWatchLimitAdvice(t *testing.T) {
	msg := watchLimitAdvice("linux", 8192, true)
	for _, want := range []string{
		"fs.inotify.max_user_watches", // verbatim, exactly as MANUAL §7 spells it
		"8192",                        // the machine's current value
		"sudo sysctl -w fs.inotify.max_user_watches=100000", // the remedy
		"per user", // why raising it is the fix and closing files is not
		"polling",  // what Synckeeper is doing meanwhile
		"restored", // and that it comes back by itself
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("advice missing %q\ngot: %s", want, msg)
		}
	}

	// An unknown value is said to be unknown, never guessed.
	unknown := watchLimitAdvice("linux", 0, false)
	if !strings.Contains(unknown, "fs.inotify.max_user_watches is unknown") {
		t.Errorf("advice with no readable value = %q, want it to say the value is unknown", unknown)
	}

	// Advising 100000 to a machine already above it would be advice to make
	// things worse.
	high := watchLimitAdvice("linux", 524288, true)
	if !strings.Contains(high, "fs.inotify.max_user_watches=1048576") {
		t.Errorf("advice at 524288 = %q, want it to suggest doubling", high)
	}

	// Off Linux ENOSPC would mean something else, and a wrong cause is worse
	// than none.
	for _, goos := range []string{"darwin", "windows", "freebsd"} {
		if got := watchLimitAdvice(goos, 8192, true); got != "" {
			t.Errorf("watchLimitAdvice(%q) = %q, want empty", goos, got)
		}
	}
}

// L4a end to end through the latch: the cause is logged once when watching is
// given up — not per failed directory, and not on every cycle.
func TestWatchLimitNamedOnceWhenLatchTrips(t *testing.T) {
	logs := captureWarnings(t)
	watchLimitHint = func() string { return watchLimitAdvice("linux", 8192, true) }
	t.Cleanup(func() { watchLimitHint = liveWatchLimitHint })

	w := &Watcher{Poll: time.Second}
	var latch failureLatch
	var fw fsWatcher = &nopBackend{}

	// Three cycles, each reporting 40 unwatchable directories — 120 failed Adds
	// in total, every one of them ENOSPC.
	latched := false
	for cycle := 0; cycle < watchFailureLatch; cycle++ {
		w.watchLimitHit.Store(true) // what refresh's ENOSPC branch does
		latched = w.latchIfNeeded(&latch, 40, &fw)
	}
	if !latched {
		t.Fatalf("latch never tripped after %d failing cycles", watchFailureLatch)
	}

	got := logs()
	// One log record, not one per failed directory: the handler writes a
	// record per line, so counting lines counts records.
	records := 0
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if strings.Contains(line, inotifyWatchesSysctl) {
			records++
		}
	}
	if records != 1 {
		t.Errorf("%s named in %d log records, want exactly 1 (once at the latch, never per directory or per cycle)\n%s",
			inotifyWatchesSysctl, records, got)
	}
	if !strings.Contains(got, "8192") || !strings.Contains(got, "sudo sysctl -w") {
		t.Errorf("the latch message does not carry the value and the remedy:\n%s", got)
	}
	// The descriptor guess is wrong here and must not appear beside the answer.
	if strings.Contains(got, "out of file descriptors?") {
		t.Errorf("latch still blamed descriptors while the kernel said ENOSPC:\n%s", got)
	}
}

// The other arm: nothing said ENOSPC, so the old descriptor wording stands and
// nothing invents an inotify cause (this is what darwin/kqueue and Windows get).
func TestLatchWithoutWatchLimitKeepsDescriptorWording(t *testing.T) {
	logs := captureWarnings(t)
	watchLimitHint = func() string { return watchLimitAdvice("linux", 8192, true) }
	t.Cleanup(func() { watchLimitHint = liveWatchLimitHint })

	w := &Watcher{Poll: time.Second}
	var latch failureLatch
	var fw fsWatcher = &nopBackend{}
	for cycle := 0; cycle < watchFailureLatch; cycle++ {
		if !w.latchIfNeeded(&latch, 7, &fw) && cycle == watchFailureLatch-1 {
			t.Fatal("latch never tripped")
		}
	}

	got := logs()
	if strings.Contains(got, inotifyWatchesSysctl) {
		t.Errorf("blamed the inotify budget with no ENOSPC observed:\n%s", got)
	}
	if !strings.Contains(got, "out of file descriptors?") {
		t.Errorf("lost the descriptor wording for a non-ENOSPC latch:\n%s", got)
	}
}

// L4b: an overflow means the kernel dropped events, so it wakes the loop —
// otherwise a `git checkout` inside the sync dir sits unsynced until the next
// poll tick. Driven straight at the error branch of the real backend's pump, so
// it executes on darwin without needing a 16384-event kernel queue to overrun.
func TestEventOverflowWakesAndIsCounted(t *testing.T) {
	captureWarnings(t) // the overflow warns; keep it out of the test output

	var wakes, overflows atomic.Int32
	b := &fsnotifyBackend{hooks: backendHooks{
		wake:     func() { wakes.Add(1) },
		overflow: func() { overflows.Add(1) },
	}}

	b.noteError(fsnotify.ErrEventOverflow)
	b.noteError(fmt.Errorf("inotify: %w", fsnotify.ErrEventOverflow)) // wrapped counts too
	if wakes.Load() != 2 || overflows.Load() != 2 {
		t.Fatalf("after 2 overflows: wakes=%d overflows=%d, want 2 and 2", wakes.Load(), overflows.Load())
	}

	// An ordinary watcher error is not a dropped-event report: it must not
	// fabricate a wake or inflate the count.
	b.noteError(errors.New("some other fsnotify error"))
	if wakes.Load() != 2 || overflows.Load() != 2 {
		t.Errorf("an unrelated error moved the counters: wakes=%d overflows=%d, want 2 and 2",
			wakes.Load(), overflows.Load())
	}

	// A backend built with no hooks at all (the scale/rebuild tests do this)
	// must not panic on the same path.
	(&fsnotifyBackend{}).noteError(fsnotify.ErrEventOverflow)
}

// L4b, the wiring: what the backend reports must reach the loop's debounce
// timer and the `stat` payload the dashboard reads. Injected through the
// newBackend seam, the way TestRebuildIsPerBackend isolates the loop from any
// real OS watcher.
func TestOverflowReachesDebounceAndStat(t *testing.T) {
	var hooks backendHooks
	newBackend = func(_ context.Context, _ string, _ func() []string, h backendHooks) (fsWatcher, int, error) {
		hooks = h
		return &nopBackend{}, 0, nil
	}
	t.Cleanup(func() { newBackend = newFSNotifyBackend })

	w := &Watcher{Eng: &engine.Engine{}, Poll: time.Minute, Debounce: time.Hour}
	w.publishIgnore(nil)
	live := newLiveState(w.Poll, w.Debounce)

	fired := make(chan struct{}, 1)
	debounce := time.AfterFunc(time.Hour, func() {
		select {
		case fired <- struct{}{}:
		default:
		}
	})
	defer debounce.Stop()

	if _, _, err := w.startNotifier(context.Background(), debounce, live); err != nil {
		t.Fatalf("startNotifier: %v", err)
	}

	// Exactly what fsnotifyBackend.noteError does on ErrEventOverflow.
	hooks.noteOverflow()
	hooks.noteWake()

	// The wake re-armed the debounce timer, which is what makes the rescan
	// immediate instead of a poll interval away.
	debounce.Reset(10 * time.Millisecond)
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("the overflow's wake never reached the sync loop's debounce timer")
	}

	s := live.snapshot(time.Now())
	if s.EventOverflows != 1 {
		t.Errorf("stat reports EventOverflows=%d, want 1", s.EventOverflows)
	}
	if !s.WakePending {
		t.Error("stat reports no pending wake after an overflow")
	}

	hooks.noteOverflow()
	if got := live.snapshot(time.Now()).EventOverflows; got != 2 {
		t.Errorf("EventOverflows=%d after a second overflow, want 2 (a running total, not a pending count)", got)
	}
	// It is a daemon-lifetime total: a cycle consuming the wake does not clear it.
	live.cycleBegin(time.Now(), 1)
	if got := live.snapshot(time.Now()).EventOverflows; got != 2 {
		t.Errorf("EventOverflows=%d after a cycle began, want 2 (only PendingChanges resets)", got)
	}
}

// The hooks are optional by construction; a backend must never have to check.
func TestBackendHooksAreOptional(t *testing.T) {
	var zero backendHooks
	zero.noteWake()
	zero.noteOverflow()
	zero.noteWatchLimit()
}
