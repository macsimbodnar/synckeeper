package engine

// Phase 7: case-collision safety on a case-insensitive filesystem (APFS).

import (
	"context"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/names"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// A case-only local rename (notes.txt -> Notes.txt) on a case-insensitive FS
// must propagate as a remote rename, not trash + re-upload. Skipped where the
// test's FS is case-sensitive (it can't express the scenario).
func TestCaseOnlyRenameBecomesMove(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	if !names.CaseInsensitiveFS(a.dir) {
		t.Skip("sync dir is on a case-sensitive filesystem; case-only rename not expressible")
	}

	a.write(t, "notes.txt", "hello")
	a.sync(t)

	a.rename(t, "notes.txt", "Notes.txt")
	res := a.sync(t)

	for _, act := range res.Plan {
		if act.Type == reconcile.TrashRemote {
			t.Fatalf("case-only rename trashed the remote instead of moving it: %+v", res.Plan)
		}
		if act.Type == reconcile.Upload {
			t.Fatalf("case-only rename re-uploaded instead of moving: %+v", res.Plan)
		}
	}

	// The remote file was renamed in place (same content, new case).
	children, err := fake.List(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	var names_ []string
	for _, c := range children {
		names_ = append(names_, c.Name)
	}
	if len(children) != 1 || children[0].Name != "Notes.txt" {
		t.Fatalf("remote should hold exactly Notes.txt, got %v", names_)
	}
}
