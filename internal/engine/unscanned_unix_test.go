//go:build unix

package engine

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// S013, non-regular flavour: a tracked file replaced by a fifo is held like a
// symlink — not trashed on Drive, not replaced by a Drive edit, and back in
// the ordinary table once it is a regular file again.
func TestS013TrackedFileReplacedByFifoIsHeld(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "doc.txt", "original")
	a.sync(t)
	b.sync(t)

	a.remove(t, "doc.txt")
	pipe := filepath.Join(a.dir, "doc.txt")
	if err := syscall.Mkfifo(pipe, 0o644); err != nil {
		t.Skipf("cannot create a fifo here: %v", err)
	}
	res := a.sync(t)
	if got := actionsOn(res.Plan, "doc.txt"); len(got) != 0 {
		t.Fatalf("a tracked file replaced by a fifo was acted on: %+v", got)
	}
	if !skipReported(res.Skips, "doc.txt") {
		t.Errorf("the fifo was not reported: %+v", res.Skips)
	}

	b.write(t, "doc.txt", "edited on b")
	b.sync(t)
	res = a.sync(t)
	if got := actionsOn(res.Plan, "doc.txt"); len(got) != 0 {
		t.Fatalf("a Drive edit was applied over the fifo: %+v", got)
	}
	if info, err := os.Lstat(pipe); err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("the fifo was replaced (err %v)", err)
	}
	if got := driveTreeOf(t, fake, root)["doc.txt"]; got != "edited on b" {
		t.Fatalf("doc.txt on Drive = %q, want b's edit untouched", got)
	}

	a.remove(t, "doc.txt")
	a.write(t, "doc.txt", "original")
	res = a.sync(t)
	if n := auditCount(res.Plan, reconcile.Upload) + auditCount(res.Plan, reconcile.UpdateRemote); n != 0 {
		t.Errorf("the restored, unchanged file was uploaded: %+v", res.Plan)
	}
	b.sync(t)
	assertConverged(t, a, b)
	if got := a.read(t, "doc.txt"); got != "edited on b" {
		t.Fatalf("doc.txt = %q, want Drive's edit", got)
	}
}
