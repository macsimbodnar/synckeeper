package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// W18.8 — a Drive folder id the plan already carries is known before the plan
// runs, not after the row that adopts it commits.
//
// Found while building W18-E. The executor learned folder ids from the
// baseline plus whatever earlier actions had committed, so at an init merge
// (empty baseline) nothing inside an adopted folder had a resolvable parent
// until that folder's Record had run. Two consequences, one deterministic and
// one a race: E's Drive-side rename lives in the MOVES stage, which runs
// before every Record, so it failed outright; a plain upload into the same
// folder merely raced the Record inside the parallel transfer stage and
// usually won because a Record is a DB write and an upload hashes a file
// first. A parent id is a fact about Drive, not about our DB, so the fix is
// to read it off the plan.
//
// The plan order below is the one a worker pool can produce: the child runs
// before the folder's own action. That makes the old behaviour fail every
// time rather than one run in many.
func TestFolderIDsComeFromThePlanNotTheBaseline(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	syncDir := filepath.Join(base, "sync")
	if err := os.MkdirAll(filepath.Join(syncDir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := statedb.Open(filepath.Join(base, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	fake := driveclient.NewFake()
	folder, err := fake.Mkdir(ctx, driveclient.FakeRootID, "Synckeeper")
	if err != nil {
		t.Fatal(err)
	}
	// The folder exists on Drive already — this is the shape an init merge
	// finds: both sides have "docs", so the plan adopts it with a Record.
	docs, err := fake.Mkdir(ctx, folder.ID, "docs")
	if err != nil {
		t.Fatal(err)
	}
	remote, err := fake.Upload(ctx, docs.ID, "theirs.txt", strings.NewReader("theirs"), 6)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(syncDir, "docs", "mine.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	x := &Executor{DB: db, Client: fake, SyncDir: syncDir,
		QuarantineDir: filepath.Join(base, "quarantine"), RootID: folder.ID}
	sum, err := x.Apply(ctx, []reconcile.Action{
		// A rename on Drive inside the folder, in the moves stage: strictly
		// before any Record can commit (W18-E's losing side steps aside here).
		{Type: reconcile.MoveRemote, RelPath: "docs/theirs.txt",
			NewRelPath: "docs/theirs (conflict box).txt", FileID: remote.ID,
			MD5: remote.MD5, Size: remote.Size},
		// The child transfer listed BEFORE the folder it lives in.
		{Type: reconcile.Upload, RelPath: "docs/mine.txt"},
		{Type: reconcile.Record, RelPath: "docs", FileID: docs.ID, IsDir: true, Version: docs.Version},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Failed != 0 {
		t.Fatalf("failed = %d, want 0 — a known folder id must not depend on action order: %v", sum.Failed, sum.Errors)
	}

	children, err := fake.List(ctx, docs.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, c := range children {
		got[c.Name] = true
	}
	// Both landed in the real folder, and the rename took: no second "docs"
	// was invented, and nothing was uploaded to the Drive root.
	for _, want := range []string{"mine.txt", "theirs (conflict box).txt"} {
		if !got[want] {
			t.Errorf("docs/ on Drive is missing %q; holds %v", want, got)
		}
	}
	if len(got) != 2 {
		t.Errorf("docs/ on Drive holds %d children, want 2: %v", len(got), got)
	}
}
