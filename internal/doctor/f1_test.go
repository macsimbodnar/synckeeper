package doctor

// W18.4 — the adversarial review's F1, end to end, kept permanently.
//
// The original reproduction (2026-07-31): three local files, the Drive folder
// renamed in the web UI, then `doctor --repair` — which resolved the folder by
// NAME, found none called "Synckeeper", created an empty one, repointed
// root_folder_id at it and left every baseline row. The next ordinary sync then
// read the whole baseline as "deleted on Drive":
//
//	repair: adopted=0 rows, root fake-1 -> fake-7, remoteMissing=5
//	plan: 2 actions, 3 local files deleted; guardBlocked=false; tree 3 -> 0
//
// The guard stayed silent because W14 narrowed it to machines with no system
// bin. This test is the whole chain — repair AND the sync after it — because
// repair alone never deleted anything; it set up the cycle that did.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

func (w *world) tree(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(w.syncDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(w.syncDir, p)
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRepairAfterDriveFolderRenameLeavesTheTreeIntact(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.write(t, "a.txt", "content a")
	w.write(t, "docs/b.txt", "content b")
	w.write(t, "docs/deep/c.txt", "content c")
	w.sync(t)
	before := w.tree(t)
	if len(before) != 3 {
		t.Fatalf("setup: %d files, want 3", len(before))
	}

	// The user renames the sync folder in the Drive web UI. Nothing is
	// deleted; every file still lives inside it, under the same file ids.
	if _, err := w.fake.Move(ctx, w.rootID, driveclient.FakeRootID, "Synckeeper-old"); err != nil {
		t.Fatal(err)
	}

	// Something looks off, so the documented recovery command is run.
	if _, err := w.doctor().Repair(ctx); err != nil {
		t.Fatal(err)
	}
	rootID, err := w.db.GetMeta(statedb.MetaRootFolderID)
	if err != nil {
		t.Fatal(err)
	}
	if rootID != w.rootID {
		t.Fatalf("repair repointed the root %s -> %s; a rename is not a new folder", w.rootID, rootID)
	}
	if got, _ := w.db.GetMeta(statedb.MetaRootFolderName); got != "Synckeeper-old" {
		t.Errorf("recorded folder name = %q, want the observed Synckeeper-old", got)
	}

	// The cycle repair's own output tells the user to run. This is where the
	// tree used to disappear.
	res := w.sync(t)
	for _, a := range res.Plan {
		if a.Type == reconcile.QuarantineLocal || a.Type == reconcile.TrashRemote {
			t.Errorf("plan contains a delete-class action after a mere rename: %s %s", a.Type, a.RelPath)
		}
	}
	after := w.tree(t)
	if len(after) != len(before) {
		t.Fatalf("tree went from %d files to %d after repair + sync", len(before), len(after))
	}
	for p, content := range before {
		if after[p] != content {
			t.Errorf("%s = %q, want %q", p, after[p], content)
		}
	}

	// And the machine is still genuinely synced, not merely undamaged.
	rep, err := w.doctor().Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Healthy() {
		t.Errorf("not healthy after a rename + repair + sync: %+v", rep)
	}
}
