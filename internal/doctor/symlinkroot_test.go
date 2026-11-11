package doctor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/names"
)

// S007: doctor over a symlinked sync_dir sees the directory the link names —
// its scan and its orphan-temp sweep both walk from the root, and a walk does
// not descend a symlinked root.
func TestDoctorFollowsSymlinkedSyncDir(t *testing.T) {
	w := newWorld(t)
	w.write(t, "a.txt", "content a")
	w.write(t, "dir/b.txt", "content b")
	realDir := w.syncDir
	link := filepath.Join(t.TempDir(), "Synckeeper")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	w.syncDir = link
	w.sync(t)

	orphan := filepath.Join(realDir, "dir", names.TempPrefix+"interrupted")
	if err := os.WriteFile(orphan, []byte("half a download"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := w.doctor().Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.MissingLocal) != 0 || len(rep.UntrackedLocal) != 0 {
		t.Errorf("doctor misread the symlinked tree: missing %v, untracked %v", rep.MissingLocal, rep.UntrackedLocal)
	}
	if len(rep.OrphanTemps) != 1 || rep.OrphanTemps[0] != "dir/"+names.TempPrefix+"interrupted" {
		t.Fatalf("OrphanTemps = %v, want the one interrupted download", rep.OrphanTemps)
	}

	rep, err = w.doctor().Repair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("repair left the orphan temp in place: %v", err)
	}
	if !rep.Healthy() {
		t.Errorf("symlinked world not healthy after repair: %+v", rep)
	}
}
