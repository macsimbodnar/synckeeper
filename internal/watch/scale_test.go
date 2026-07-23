package watch

// W1-scale acceptance (W3.3): a >=50k-file tree must sync under the daemon
// without fd exhaustion, and the watcher must degrade to polling — never crash —
// when it cannot cover the tree. Gated by SYNCKEEPER_SCALE_FILES because
// creating and syncing tens of thousands of files is slow; the acceptance gate
// is SYNCKEEPER_SCALE_FILES=50000. On macOS fsnotify's kqueue holds one
// descriptor per watched file, so at this scale it legitimately runs out and
// latches to polling (R15 drives the full degrade->sync->recover loop); the
// FSEvents backend holds no per-file descriptors and never exhausts them
// (TestFSEventsScaleNoFDExhaustion, darwin+cgo).

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/engine"
)

// scaleFiles reads the acceptance gate, or skips.
func scaleFiles(t *testing.T) int {
	t.Helper()
	v := os.Getenv("SYNCKEEPER_SCALE_FILES")
	if v == "" {
		t.Skip("set SYNCKEEPER_SCALE_FILES (50000 for the W1-scale acceptance gate)")
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		t.Fatalf("SYNCKEEPER_SCALE_FILES=%q: %v", v, err)
	}
	return n
}

// buildScaleTree writes n tiny files spread across directories (perDir each)
// under root and returns the directory count.
func buildScaleTree(t *testing.T, root string, n int) (dirs int) {
	t.Helper()
	const perDir = 100
	for i := 0; i < n; i += perDir {
		dir := filepath.Join(root, fmt.Sprintf("d%06d", dirs))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		dirs++
		for j := 0; j < perDir && i+j < n; j++ {
			p := filepath.Join(dir, fmt.Sprintf("f%03d.txt", j))
			if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dirs
}

func TestScale(t *testing.T) {
	n := scaleFiles(t)
	fake, root := newWorld(t)
	m := newMachine(t, "scale", fake, root)
	dirs := buildScaleTree(t, m.dir, n)
	var lim syscall.Rlimit
	syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim)
	t.Logf("built %d files across %d dirs; open-file soft limit %d", n, dirs, lim.Cur)

	// The engine handles the scale: a full sync converges and the next cycle is
	// idle. This exercises the scan (a stat + md5 open per file) and reconcile
	// over the whole tree.
	res, err := m.eng.Sync(context.Background(), engine.Options{})
	if err != nil {
		t.Fatalf("scale sync: %v", err)
	}
	if res.Failed != 0 {
		t.Fatalf("scale sync: %d failed actions, first few: %v", res.Failed, res.Errors[:min(3, len(res.Errors))])
	}
	if len(res.Plan) < n {
		t.Fatalf("plan had %d actions, want at least %d uploads", len(res.Plan), n)
	}
	t.Logf("synced %d planned actions; executed %d", len(res.Plan), res.Executed)
	res2, err := m.eng.Sync(context.Background(), engine.Options{})
	if err != nil {
		t.Fatalf("second scale cycle: %v", err)
	}
	if len(res2.Plan) != 0 {
		t.Fatalf("second cycle planned %d actions, want steady state", len(res2.Plan))
	}

	// The fsnotify backend over the same tree: at 50k on macOS its per-file
	// kqueue descriptors run out, so some directories cannot be watched. That is
	// graceful, not fatal — the daemon latches to polling (R15) and keeps
	// syncing. Confirm creation never errors and, when fd pressure does occur,
	// that the failure latch trips.
	fdLimitOnce.Do(raiseFDLimit)
	fb, failed, err := newFSNotifyBackend(context.Background(), m.dir,
		func() []string { return nil }, func() {})
	if err != nil {
		t.Fatalf("fsnotify backend over %d files: %v", n, err)
	}
	fb.close()
	t.Logf("fsnotify: %d of %d directories could not be watched (fd pressure)", failed, dirs)
	if failed > 0 {
		var l failureLatch
		tripped := false
		for i := 0; i < watchFailureLatch; i++ {
			tripped = l.note(failed)
		}
		if !tripped {
			t.Errorf("failure latch did not trip after %d refreshes reporting failed=%d", watchFailureLatch, failed)
		}
	}
}
