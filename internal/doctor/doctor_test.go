package doctor

// Includes fault test F4: state DB deleted entirely -> `doctor --repair`
// rebuilds the baseline by md5 matching and the next sync propagates no
// deletions and re-transfers nothing that already matches.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/config"
	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/engine"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

type world struct {
	fake    *driveclient.Fake
	rootID  string
	base    string
	syncDir string
	db      *statedb.DB
	cfg     config.Config
}

func newWorld(t *testing.T) *world {
	t.Helper()
	fake := driveclient.NewFake()
	folder, err := fake.Mkdir(context.Background(), driveclient.FakeRootID, "Synckeeper")
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	syncDir := filepath.Join(base, "sync")
	if err := os.MkdirAll(syncDir, 0o755); err != nil {
		t.Fatal(err)
	}
	w := &world{fake: fake, rootID: folder.ID, base: base, syncDir: syncDir, cfg: config.Default()}
	w.cfg.Engine.MachineName = "doc_test"
	w.openDB(t)
	return w
}

func (w *world) openDB(t *testing.T) {
	t.Helper()
	db, err := statedb.Open(filepath.Join(w.base, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	w.db = db
	token, err := w.fake.StartPageToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Mirrors init: token + root id. (Deleted-DB tests wipe this again.)
	w.db.SetMeta(statedb.MetaPageToken, token)
	w.db.SetMeta(statedb.MetaRootFolderID, w.rootID)
}

func (w *world) engine() *engine.Engine {
	return &engine.Engine{
		DB: w.db, Client: w.fake, Cfg: w.cfg, SyncDir: w.syncDir,
		QuarantineDir: filepath.Join(w.base, "quarantine"), RootID: w.rootID,
	}
}

func (w *world) doctor() *Doctor {
	return &Doctor{DB: w.db, Client: w.fake, Cfg: w.cfg, SyncDir: w.syncDir}
}

func (w *world) write(t *testing.T, rel, content string) {
	t.Helper()
	p := filepath.Join(w.syncDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (w *world) sync(t *testing.T) *engine.Result {
	t.Helper()
	res, err := w.engine().Sync(context.Background(), engine.Options{})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Failed > 0 {
		t.Fatalf("sync failed actions: %v", res.Errors)
	}
	return res
}

func TestCheckHealthyAfterSync(t *testing.T) {
	w := newWorld(t)
	w.write(t, "a.txt", "content a")
	w.write(t, "dir/b.txt", "content b")
	w.sync(t)

	rep, err := w.doctor().Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Healthy() {
		t.Errorf("freshly synced world should be healthy: %+v", rep)
	}
	if rep.TrackedItems != 3 {
		t.Errorf("tracked = %d, want 3", rep.TrackedItems)
	}
}

func TestCheckReportsDivergence(t *testing.T) {
	w := newWorld(t)
	w.write(t, "tracked.txt", "synced")
	w.sync(t)

	w.write(t, "tracked.txt", "modified after sync")
	w.write(t, "untracked.txt", "never synced")
	if _, err := w.fake.Upload(context.Background(), w.rootID, "remote-only.txt", strings.NewReader("drive side"), 10); err != nil {
		t.Fatal(err)
	}

	rep, err := w.doctor().Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.LocalModified) != 1 || rep.LocalModified[0] != "tracked.txt" {
		t.Errorf("LocalModified = %v", rep.LocalModified)
	}
	if len(rep.UntrackedLocal) != 1 || rep.UntrackedLocal[0] != "untracked.txt" {
		t.Errorf("UntrackedLocal = %v", rep.UntrackedLocal)
	}
	if len(rep.RemoteOnly) != 1 || rep.RemoteOnly[0] != "remote-only.txt" {
		t.Errorf("RemoteOnly = %v", rep.RemoteOnly)
	}
	if rep.Healthy() {
		t.Error("divergent world reported healthy")
	}
}

// F4: the state DB is deleted entirely.
func TestF4LostDBRepairedByMD5Match(t *testing.T) {
	w := newWorld(t)
	w.write(t, "keep/deep.txt", "deep content here")
	w.write(t, "root.txt", "root content")
	w.write(t, "local-only.txt", "never made it to drive")
	w.sync(t)
	// local-only.txt DID make it to Drive during that sync; recreate a
	// genuinely one-sided pair after the fact:
	if _, err := w.fake.Upload(context.Background(), w.rootID, "remote-only.txt", strings.NewReader("drive only"), 10); err != nil {
		t.Fatal(err)
	}
	w.write(t, "new-local.txt", "disk only")

	// Disaster: the DB is gone. New empty DB, no meta at all.
	w.db.Close()
	if err := os.Remove(filepath.Join(w.base, "state.db")); err != nil {
		t.Fatal(err)
	}
	db, err := statedb.Open(filepath.Join(w.base, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	w.db = db

	// Sync without repair cannot even start (no page token).
	if _, err := w.engine().Sync(context.Background(), engine.Options{}); err == nil {
		t.Fatal("sync with a lost DB should fail until doctor --repair runs")
	}

	rep, err := w.doctor().Repair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// keep/, keep/deep.txt, root.txt, local-only.txt all match by md5.
	if rep.Adopted != 4 {
		t.Errorf("adopted = %d, want 4: %+v", rep.Adopted, rep)
	}

	// The next sync must produce zero deletions and no duplicate uploads.
	res, err := w.engine().Sync(context.Background(), engine.Options{})
	if err != nil {
		t.Fatalf("post-repair sync: %v", err)
	}
	for _, a := range res.Plan {
		if a.Type == reconcile.TrashRemote || a.Type == reconcile.QuarantineLocal {
			t.Errorf("post-repair sync planned a deletion: %+v", a)
		}
	}
	if res.Failed > 0 {
		t.Fatalf("post-repair sync failures: %v", res.Errors)
	}

	// Converged: one-sided files crossed over, nothing duplicated.
	children, _ := w.fake.List(context.Background(), w.rootID)
	seen := map[string]int{}
	for _, c := range children {
		seen[c.Name]++
	}
	for _, name := range []string{"root.txt", "local-only.txt", "remote-only.txt", "new-local.txt", "keep"} {
		if seen[name] != 1 {
			t.Errorf("drive has %d of %q, want exactly 1", seen[name], name)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(w.syncDir, "remote-only.txt")); err != nil || string(raw) != "drive only" {
		t.Errorf("remote-only.txt locally = %q, %v", raw, err)
	}

	rep, err = w.doctor().Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Healthy() {
		t.Errorf("world should be healthy after repair + sync: %+v", rep)
	}
}

func TestRepairIsIdempotentOnHealthyWorld(t *testing.T) {
	w := newWorld(t)
	w.write(t, "solid.txt", "unchanging")
	w.sync(t)

	before, _ := w.db.AllItems()
	rep, err := w.doctor().Repair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	after, _ := w.db.AllItems()
	if len(before) != len(after) {
		t.Errorf("repair changed row count: %d -> %d", len(before), len(after))
	}
	if res := w.sync(t); len(res.Plan) != 0 {
		t.Errorf("sync after no-op repair planned %d actions", len(res.Plan))
	}
	_ = rep
}
