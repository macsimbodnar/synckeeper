package main

// Regression tests written by the 2026-11-11 adversarial audit
// (adocs/audit/2026-11-11_adversarial.md). Each one demonstrates an OPEN
// finding and therefore FAILS at the audited commit (1cea1b7). Gated behind
// SYNCKEEPER_AUDIT_REPRO=1 so `make test` stays green until the fix lands; the
// fixing step removes the gate for its test, red first.
//
//	SYNCKEEPER_AUDIT_REPRO=1 go test ./cmd/synckeeper -run TestAudit20261111 -v
//
// Both tests replay `init`'s own steps in `init`'s own order (init.go RunE:
// initialize → os.MkdirAll(syncDir) → eng.Sync(PreferNewer)); the command
// itself cannot be driven here because its sign-in has no test seam.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/config"
	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/engine"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
	"github.com/macsimbodnar/synckeeper/internal/trash"
)

func auditRepro(t *testing.T) {
	t.Helper()
	if os.Getenv("SYNCKEEPER_AUDIT_REPRO") == "" {
		t.Skip("open audit finding; set SYNCKEEPER_AUDIT_REPRO=1 to reproduce (fails until fixed)")
	}
}

// auditInstall is one machine: a state DB, a sync dir, and the three steps
// `init` performs.
type auditInstall struct {
	t       *testing.T
	fake    *driveclient.Fake
	db      *statedb.DB
	cfg     config.Config
	base    string
	syncDir string
	bin     *trash.Fake
}

func newAuditInstall(t *testing.T) *auditInstall {
	t.Helper()
	base := t.TempDir()
	return &auditInstall{t: t, fake: driveclient.NewFake(), db: openTestDB(t), cfg: config.Default(),
		base: base, syncDir: filepath.Join(base, "Synckeeper"), bin: trash.NewFake(filepath.Join(base, "bin"))}
}

func (in *auditInstall) engine(rootID string) *engine.Engine {
	return &engine.Engine{DB: in.db, Client: in.fake, Cfg: in.cfg, SyncDir: in.syncDir,
		QuarantineDir: filepath.Join(in.base, "quarantine"), RootID: rootID, Trash: in.bin}
}

// runInit is init.go's RunE from `initialize` to the merge.
func (in *auditInstall) runInit() *engine.Result {
	in.t.Helper()
	ctx := context.Background()
	rootID, err := initialize(ctx, in.fake, in.db, in.cfg) // init.go:96
	if err != nil {
		in.t.Fatal(err)
	}
	if err := os.MkdirAll(in.syncDir, 0o755); err != nil { // init.go:105
		in.t.Fatal(err)
	}
	res, err := in.engine(rootID).Sync(ctx, engine.Options{PreferNewer: true}) // init.go:128
	if err != nil {
		in.t.Fatalf("init merge: %v", err)
	}
	if res.Failed > 0 {
		in.t.Fatalf("init merge: %d failed: %v", res.Failed, res.Errors)
	}
	return res
}

func (in *auditInstall) write(rel, content string) {
	in.t.Helper()
	p := filepath.Join(in.syncDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		in.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		in.t.Fatal(err)
	}
}

func (in *auditInstall) rootID() string {
	in.t.Helper()
	id, err := in.db.GetMeta(statedb.MetaRootFolderID)
	if err != nil {
		in.t.Fatal(err)
	}
	return id
}

// driveFiles counts the non-trashed files in the Drive folder.
func (in *auditInstall) driveFiles() int {
	in.t.Helper()
	n := 0
	queue := []string{in.rootID()}
	for len(queue) > 0 {
		kids, err := in.fake.List(context.Background(), queue[0])
		if err != nil {
			in.t.Fatal(err)
		}
		queue = queue[1:]
		for _, k := range kids {
			if k.IsDir() {
				queue = append(queue, k.ID)
			} else {
				n++
			}
		}
	}
	return n
}

// localFiles counts the files in the sync dir.
func (in *auditInstall) localFiles() int {
	in.t.Helper()
	n := 0
	filepath.WalkDir(in.syncDir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

func auditCount(plan []reconcile.Action, typ reconcile.Type) int {
	n := 0
	for _, a := range plan {
		if a.Type == typ {
			n++
		}
	}
	return n
}

// F02 — `init` over an existing state DB whose sync folder is gone empties the
// Drive folder.
//
// MANUAL §1: "`init` never deletes, and is safe to re-run." MANUAL §6:
// "Deleting the sync folder itself is never a deletion — it's recreated and
// your files downloaded back." The engine keeps that promise only when IT
// finds the folder missing (guards.EnsureSyncDir recreates it and resets the
// baseline). `init` creates the folder itself first (init.go:105, before the
// merge at :128), so the engine finds an existing, empty directory beside a
// full baseline — which it reads as "the user deleted everything" and
// propagates to Drive.
func TestAudit20261111_F02_InitWithMissingSyncDirEmptiesDrive(t *testing.T) {
	auditRepro(t)
	in := newAuditInstall(t)

	// A working install: three files, synced.
	in.write("a.txt", "a")
	in.write("docs/b.txt", "b")
	in.write("docs/c.txt", "c")
	in.runInit()
	if got := in.driveFiles(); got != 3 {
		t.Fatalf("setup: want 3 files on Drive, have %d", got)
	}

	// The sync folder is lost — deleted by mistake, a replaced disk, a new
	// machine that got its config dir restored from a backup but not the data.
	// The state DB survives. The user runs the documented recovery command.
	if err := os.RemoveAll(in.syncDir); err != nil {
		t.Fatal(err)
	}
	res := in.runInit()

	if trashed, left := auditCount(res.Plan, reconcile.TrashRemote), in.driveFiles(); left != 3 || trashed > 0 {
		t.Fatalf("`init` deleted from Drive: %d trash_remote actions planned, %d of 3 files left in the Drive folder, %d of 3 back on disk (want 3 and 3: recreated and downloaded back)",
			trashed, left, in.localFiles())
	}
}

// flakyList fails every List call from the nth on, as a dropped connection
// (or Ctrl-C, or an exhausted rate limit) part-way through a walk would.
type flakyList struct {
	driveclient.Client
	failFrom int
	calls    int
}

func (f *flakyList) List(ctx context.Context, parentID string) ([]driveclient.File, error) {
	f.calls++
	if f.calls >= f.failFrom {
		return nil, errors.New("dial tcp: connection reset by peer")
	}
	return f.Client.List(ctx, parentID)
}

// F03 — An interrupted mirror rebuild is read as a mass deletion on Drive.
//
// A re-run of `init` and `doctor --repair` both call
// remotedelta.ForceFullWalk, which empties remote_nodes and refills it one
// List call at a time, outside any transaction, while the "initial walk done"
// marker stays set. If the walk stops part-way, the mirror is left holding
// only the folders walked so far. The next ordinary cycle trusts it: every
// baseline row missing from the half-built mirror is "deleted on Drive", and
// the local copies are moved to the system bin. Drive still holds every file.
func TestAudit20261111_F03_InterruptedMirrorRebuildBinsLocalFiles(t *testing.T) {
	auditRepro(t)
	in := newAuditInstall(t)
	ctx := context.Background()

	in.write("top.txt", "top")
	for _, d := range []string{"docs", "photos", "work"} {
		for _, f := range []string{"1.txt", "2.txt", "3.txt"} {
			in.write(d+"/"+f, d+f)
		}
	}
	in.runInit()
	// A second, idle cycle: the change feed has now reported this machine's
	// own uploads and the page token is past them — the steady state of any
	// install older than one poll interval.
	if res, err := in.engine(in.rootID()).Sync(ctx, engine.Options{}); err != nil || len(res.Plan) != 0 {
		t.Fatalf("setup: second cycle should be idle: err=%v plan=%d", err, len(res.Plan))
	}
	if in.driveFiles() != 10 || in.localFiles() != 10 {
		t.Fatalf("setup: want 10 files on both sides, have %d on Drive and %d on disk", in.driveFiles(), in.localFiles())
	}

	// `init` is re-run ("safe to re-run") and the connection drops during its
	// mirror rebuild: the root listing succeeds, the first subfolder's does not.
	flaky := &flakyList{Client: in.fake, failFrom: 2}
	if _, err := initialize(ctx, flaky, in.db, in.cfg); err == nil {
		t.Fatal("setup: the interrupted init should have reported its error")
	}

	// Nothing changed on Drive and nothing changed on disk. The daemon comes
	// back up and runs its next cycle, which must plan no deletion at all.
	res, err := in.engine(in.rootID()).Sync(ctx, engine.Options{})
	if err != nil {
		t.Fatalf("cycle after the interrupted rebuild: %v", err)
	}
	if n := auditCount(res.Plan, reconcile.QuarantineLocal); n > 0 || in.localFiles() != 10 {
		t.Fatalf("an interrupted mirror rebuild was read as a deletion on Drive: "+
			"%d local delete actions planned, %d of 10 local files left on disk, %d of 10 still on Drive",
			n, in.localFiles(), in.driveFiles())
	}
}
