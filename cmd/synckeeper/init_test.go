package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

func openTestDB(t *testing.T) *statedb.DB {
	t.Helper()
	db, err := statedb.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestInitializeCreatesFolderAndMeta(t *testing.T) {
	ctx := context.Background()
	fake := driveclient.NewFake()
	db := openTestDB(t)
	cfg := config.Default()

	if _, err := initialize(ctx, fake, db, cfg); err != nil {
		t.Fatal(err)
	}

	rootID, err := db.GetMeta(statedb.MetaRootFolderID)
	if err != nil {
		t.Fatal(err)
	}
	folder, err := fake.Get(ctx, rootID)
	if err != nil {
		t.Fatal(err)
	}
	if !folder.IsDir() || folder.Name != cfg.Drive.FolderName {
		t.Errorf("root folder = %+v, want folder named %q", folder, cfg.Drive.FolderName)
	}
	if tok, err := db.GetMeta(statedb.MetaPageToken); err != nil || tok == "" {
		t.Errorf("page_token = %q, %v; want non-empty", tok, err)
	}
	if id, err := db.GetMeta(statedb.MetaMachineID); err != nil || id == "" {
		t.Errorf("machine_id = %q, %v; want non-empty", id, err)
	}
}

func TestInitializeAdoptsExistingFolder(t *testing.T) {
	ctx := context.Background()
	fake := driveclient.NewFake()
	existing, err := fake.Mkdir(ctx, driveclient.FakeRootID, "Synckeeper")
	if err != nil {
		t.Fatal(err)
	}
	db := openTestDB(t)

	// An existing but EMPTY folder is reused without --adopt (no content to
	// merge, so no risk of joining someone else's data unknowingly).
	if _, err := initialize(ctx, fake, db, config.Default()); err != nil {
		t.Fatal(err)
	}
	rootID, _ := db.GetMeta(statedb.MetaRootFolderID)
	if rootID != existing.ID {
		t.Errorf("root folder id = %s, want existing %s (must not create a duplicate)", rootID, existing.ID)
	}
	children, _ := fake.List(ctx, driveclient.FakeRootID)
	folders := 0
	for _, c := range children {
		if c.IsDir() && c.Name == "Synckeeper" {
			folders++
		}
	}
	if folders != 1 {
		t.Errorf("found %d Synckeeper folders at root, want 1", folders)
	}
}

func TestInitializeKeepsMachineID(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if _, err := initialize(ctx, driveclient.NewFake(), db, config.Default()); err != nil {
		t.Fatal(err)
	}
	first, _ := db.GetMeta(statedb.MetaMachineID)
	if _, err := initialize(ctx, driveclient.NewFake(), db, config.Default()); err != nil {
		t.Fatal(err)
	}
	second, _ := db.GetMeta(statedb.MetaMachineID)
	if first == "" || first != second {
		t.Errorf("machine_id changed across re-init: %q -> %q", first, second)
	}
}

// W18-B: `init` no longer refuses a non-empty Drive folder — joining one is a
// union merge that cannot delete (spec §11), so making the user assert the
// intention with --adopt only ever taught them to pass a flag unread. The
// replacement property is stronger and is what a user would notice: joining
// succeeds, resolves the EXISTING folder rather than minting one, and is
// idempotent across repeated runs.
func TestInitializeJoinsNonEmptyFolderIdempotently(t *testing.T) {
	ctx := context.Background()
	fake := driveclient.NewFake()
	folder, err := fake.Mkdir(ctx, driveclient.FakeRootID, "Synckeeper")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fake.Upload(ctx, folder.ID, "existing.txt", strings.NewReader("hi"), 2); err != nil {
		t.Fatal(err)
	}

	db := openTestDB(t)
	got, err := initialize(ctx, fake, db, config.Default())
	if err != nil {
		t.Fatalf("init must join a non-empty folder, not refuse: %v", err)
	}
	if got != folder.ID {
		t.Errorf("resolved %s, want the existing folder %s", got, folder.ID)
	}

	// Re-running changes nothing: same folder, no second folder on Drive.
	again, err := initialize(ctx, fake, db, config.Default())
	if err != nil {
		t.Fatalf("re-running init must be a no-op, not an error: %v", err)
	}
	if again != folder.ID {
		t.Errorf("second run resolved %s, want %s", again, folder.ID)
	}
	children, err := fake.List(ctx, driveclient.FakeRootID)
	if err != nil {
		t.Fatal(err)
	}
	folders := 0
	for _, c := range children {
		if c.IsDir() && c.Name == "Synckeeper" {
			folders++
		}
	}
	if folders != 1 {
		t.Errorf("%d folders named Synckeeper on Drive, want 1 — re-init must not mint another", folders)
	}
}

// R3 regression (2026-07-17, testing.md): a re-run of `init` must rebuild the
// remote mirror. Resetting only the page token silently skipped every remote
// change made since the last consumed batch: the stale mirror said "remote
// unchanged", the newer revision was never downloaded, and a later local
// edit would have uploaded over it — invisible divergence.
func TestR3ForceReinitSeesPriorRemoteChanges(t *testing.T) {
	ctx := context.Background()
	fake := driveclient.NewFake()
	db := openTestDB(t)
	cfg := config.Default()

	rootID, err := initialize(ctx, fake, db, cfg)
	if err != nil {
		t.Fatal(err)
	}

	// A remote file appears and one sync consumes it.
	remote, err := fake.Upload(ctx, rootID, "f.txt", strings.NewReader("v1"), 2)
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	syncDir := filepath.Join(base, "sync")
	if err := os.MkdirAll(syncDir, 0o755); err != nil {
		t.Fatal(err)
	}
	eng := &engine.Engine{DB: db, Client: fake, Cfg: cfg, SyncDir: syncDir,
		QuarantineDir: filepath.Join(base, "quarantine"), RootID: rootID}
	if res, err := eng.Sync(ctx, engine.Options{}); err != nil || res.Failed > 0 {
		t.Fatalf("first sync: err=%v result=%+v", err, res)
	}

	// The file changes remotely while this machine is not syncing…
	if _, err := fake.Update(ctx, remote.ID, strings.NewReader("v2-remote"), 9); err != nil {
		t.Fatal(err)
	}
	// …then the user re-inits and syncs. Since W18-B that needs no flags:
	// `init` is idempotent and always merges.
	if _, err := initialize(ctx, fake, db, cfg); err != nil {
		t.Fatal(err)
	}
	if res, err := eng.Sync(ctx, engine.Options{}); err != nil || res.Failed > 0 {
		t.Fatalf("post-reinit sync: err=%v result=%+v", err, res)
	}

	got, err := os.ReadFile(filepath.Join(syncDir, "f.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v2-remote" {
		t.Errorf("f.txt = %q after re-init; the pre-reinit remote edit was silently dropped (want %q)", got, "v2-remote")
	}
}

func TestInitializeSurfacesClientErrors(t *testing.T) {
	// A fake with no root folder makes List fail; initialize must propagate.
	db := openTestDB(t)
	_, err := initialize(context.Background(), brokenClient{}, db, config.Default())
	if err == nil {
		t.Fatal("want error from broken client")
	}
	if _, metaErr := db.GetMeta(statedb.MetaRootFolderID); !errors.Is(metaErr, statedb.ErrNotFound) {
		t.Error("root_folder_id was written despite failure")
	}
}

type brokenClient struct{ driveclient.Client }

func (brokenClient) List(context.Context, string) ([]driveclient.File, error) {
	return nil, errors.New("boom")
}

// W7-L1: the machine name is asked for on every `init`, because it is baked
// into conflict-copy filenames on every other machine and was previously
// invisible. Precedence: --machine-name > the existing config name on a re-run
// > the sanitized OS hostname > the named "unknown_machine" fallback.
func TestResolveMachineName(t *testing.T) {
	tests := []struct {
		name        string
		current     string
		created     bool
		flag        string
		interactive bool
		input       string
		hostname    string // "" means the OS has no usable name
		want        string
		wantOut     string // substring; "" means "nothing printed"
	}{
		{
			name: "flag wins over the prompt", current: "old_box", created: false,
			flag: "from_flag", interactive: true, input: "typed\n", hostname: "pop-os",
			want: "from_flag",
		},
		{
			name: "flag wins over the hostname without a terminal", created: true,
			flag: "from_flag", hostname: "pop-os", want: "from_flag",
		},
		{
			name: "flag is sanitized", created: true, flag: "my box/2", hostname: "pop-os",
			want: "my_box2",
		},
		{
			name: "first run defaults to the hostname", created: true,
			interactive: true, input: "\n", hostname: "pop-os",
			want: "pop-os", wantOut: "Machine name [pop-os]: ",
		},
		{
			name:    "re-run defaults to the existing name, not the hostname",
			current: "max_mbp", created: false, interactive: true, input: "\n", hostname: "pop-os",
			want: "max_mbp", wantOut: "Machine name [max_mbp]: ",
		},
		{
			name: "a typed answer is sanitized", current: "max_mbp", created: false,
			interactive: true, input: "Max's Laptop\n", hostname: "pop-os",
			want: "Maxs_Laptop", wantOut: "Machine name [max_mbp]: ",
		},
		{
			name: "non-interactive takes the default without prompting", created: true,
			interactive: false, input: "typed\n", hostname: "pop-os",
			want: "pop-os", wantOut: "",
		},
		{
			name: "no hostname, no terminal: named fallback", created: true,
			interactive: false, hostname: "",
			want: "unknown_machine", wantOut: "engine.machine_name",
		},
		{
			name: "no hostname: the prompt shows no default", created: true,
			interactive: true, input: "chosen\n", hostname: "",
			want: "chosen", wantOut: "Machine name: ",
		},
		{
			name: "no hostname, empty answer: named fallback", created: true,
			interactive: true, input: "\n", hostname: "",
			want: "unknown_machine", wantOut: "engine.machine_name",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saved := osMachineName
			osMachineName = func() (string, bool) {
				if tt.hostname == "" {
					return "unknown_machine", false
				}
				return tt.hostname, true
			}
			t.Cleanup(func() { osMachineName = saved })

			var out strings.Builder
			got := resolveMachineName(strings.NewReader(tt.input), &out, tt.current, tt.created, tt.flag, tt.interactive)
			if got != tt.want {
				t.Errorf("resolveMachineName() = %q, want %q", got, tt.want)
			}
			if tt.wantOut == "" {
				if out.String() != "" {
					t.Errorf("want nothing printed, got %q", out.String())
				}
			} else if !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("output %q missing %q", out.String(), tt.wantOut)
			}
		})
	}
}

// The resolved name must always satisfy the config validator, whatever the
// user types — it is written straight back into config.toml.
func TestResolveMachineNameAlwaysValid(t *testing.T) {
	for _, answer := range []string{"a/b\n", "///\n", "  \n", "..\n", "ok-1\n"} {
		var out strings.Builder
		got := resolveMachineName(strings.NewReader(answer), &out, "", true, "", true)
		if got == "" || got != config.SanitizeMachineName(got) {
			t.Errorf("answer %q resolved to %q, which config.validate() rejects", answer, got)
		}
	}
}

// S008 (audit 2026-11-11 F02): the F5 acceptance — a vanished sync dir is
// recreated and re-downloaded with the remote untouched — run through `init`'s
// own steps rather than the engine alone. `init` used to create the folder
// before the merge, so the engine never saw it missing, never reset the
// baseline, and read the empty folder as everything deleted.
func TestS008InitRecreatesVanishedSyncDir(t *testing.T) {
	in := newAuditInstall(t)
	in.write("precious.txt", "do not lose")
	in.write("docs/nested.txt", "also precious")
	in.runInit()

	// The folder is lost; state.db survives; the user runs `init`.
	if err := os.RemoveAll(in.syncDir); err != nil {
		t.Fatal(err)
	}
	res := in.runInit()

	for _, a := range res.Plan {
		switch a.Type {
		case reconcile.TrashRemote, reconcile.QuarantineLocal, reconcile.Upload:
			t.Errorf("init over a vanished sync dir planned %s %s; want downloads only, the remote untouched", a.Type, a.RelPath)
		}
	}
	if got := in.driveFiles(); got != 2 {
		t.Fatalf("%d of 2 files left on Drive", got)
	}
	for rel, want := range map[string]string{"precious.txt": "do not lose", "docs/nested.txt": "also precious"} {
		got, err := os.ReadFile(filepath.Join(in.syncDir, filepath.FromSlash(rel)))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q (err %v), want %q downloaded back", rel, got, err, want)
		}
	}
	// Genuinely synced again, not merely undamaged.
	if res, err := in.engine(in.rootID()).Sync(context.Background(), engine.Options{}); err != nil || len(res.Plan) != 0 {
		t.Errorf("next cycle: err=%v, %d actions planned, want steady state", err, len(res.Plan))
	}
}

// With `init` no longer creating the sync dir itself, a first run still gets
// one — parents included — from the engine, and the Drive content lands in it.
func TestS008InitFirstRunCreatesSyncDir(t *testing.T) {
	first := newAuditInstall(t)
	first.write("from-drive.txt", "remote")
	first.runInit()

	// A second machine joins: fresh state DB, sync dir and its parent absent.
	joiner := newAuditInstall(t)
	joiner.fake = first.fake
	joiner.syncDir = filepath.Join(joiner.base, "not", "yet", "Synckeeper")
	joiner.runInit()

	if info, err := os.Stat(joiner.syncDir); err != nil || !info.IsDir() {
		t.Fatalf("sync dir after first init: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(joiner.syncDir, "from-drive.txt")); err != nil || string(got) != "remote" {
		t.Errorf("from-drive.txt = %q (err %v), want it downloaded", got, err)
	}
}

// A sync dir that is a symlink to nothing (its volume unmounted) stops `init`
// with the engine's own message naming link and target — before S008 `init`
// failed first on its own mkdir with an opaque "file exists". Nothing is
// created and nothing leaves Drive.
func TestS008InitDanglingSymlinkSyncDirNamesTheLink(t *testing.T) {
	in := newAuditInstall(t)
	in.write("a.txt", "a")
	in.runInit()

	if err := os.RemoveAll(in.syncDir); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(in.base, "unmounted", "Synckeeper")
	if err := os.Symlink(target, in.syncDir); err != nil {
		t.Fatal(err)
	}
	_, err := initMerge(context.Background(), in.engine(""), func(string) {})
	if err == nil || !strings.Contains(err.Error(), "is a symlink to "+target) {
		t.Fatalf("init over a dangling symlink: err = %v, want the link and its target named", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Errorf("the link's target was created (stat err %v); it must stay absent", err)
	}
	if info, err := os.Lstat(in.syncDir); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link itself was replaced or removed (err %v)", err)
	}
	if got := in.driveFiles(); got != 1 {
		t.Errorf("%d of 1 files left on Drive", got)
	}
}

// killAtList fails the at-th List call, as a dropped connection, Ctrl-C or a
// kill part-way through a walk would. When crash is set it first takes the
// DB's crash image: the committed state a process killed at that instant
// leaves on disk, independent of anything the error path does afterwards.
type killAtList struct {
	driveclient.Client
	at, calls int
	crash     func()
}

func (k *killAtList) List(ctx context.Context, parentID string) ([]driveclient.File, error) {
	k.calls++
	if k.calls == k.at {
		if k.crash != nil {
			k.crash()
		}
		return nil, errors.New("dial tcp: connection reset by peer")
	}
	return k.Client.List(ctx, parentID)
}

// crashImage copies the committed state of the DB at src to dst.
func crashImage(t *testing.T, src, dst string) {
	t.Helper()
	h, err := sql.Open("sqlite", src)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if _, err := h.Exec(`vacuum into ?`, dst); err != nil {
		t.Fatal(err)
	}
}

// S009 (audit 2026-11-11 F03): a re-run `init` whose mirror rebuild stops at
// ANY List call — by error, or by process death — leaves a state the next
// cycle reads as what it is: nothing changed on either side. Before the fix,
// every baseline row the partial mirror had not refilled was planned as
// deleted on Drive.
func TestS009InitRebuildInterruptedAtEveryList(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) (*auditInstall, string) {
		t.Helper()
		in := newAuditInstall(t)
		path := filepath.Join(in.base, "state.db")
		db, err := statedb.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		in.db = db
		in.write("top.txt", "top")
		for _, d := range []string{"docs", "photos", "work/deep"} {
			for _, f := range []string{"1.txt", "2.txt", "3.txt"} {
				in.write(d+"/"+f, d+f)
			}
		}
		in.runInit()
		if res, err := in.engine(in.rootID()).Sync(ctx, engine.Options{}); err != nil || len(res.Plan) != 0 {
			t.Fatalf("setup: second cycle should be idle: err=%v plan=%d", err, len(res.Plan))
		}
		return in, path
	}

	// Every List a clean re-run makes is a point to interrupt at.
	clean, _ := setup(t)
	counter := &killAtList{Client: clean.fake}
	if _, err := initialize(ctx, counter, clean.db, clean.cfg); err != nil {
		t.Fatal(err)
	}
	if counter.calls < 5 {
		t.Fatalf("a clean re-run made %d List calls; the tree has 5 folders to walk", counter.calls)
	}

	for at := 1; at <= counter.calls; at++ {
		for _, crash := range []bool{false, true} {
			t.Run(fmt.Sprintf("list%d/crash=%v", at, crash), func(t *testing.T) {
				in, path := setup(t)
				k := &killAtList{Client: in.fake, at: at}
				image := filepath.Join(in.base, "crash.db")
				if crash {
					k.crash = func() { crashImage(t, path, image) }
				}
				if _, err := initialize(ctx, k, in.db, in.cfg); err == nil {
					t.Fatal("the interrupted init should have reported its error")
				}
				if crash {
					db, err := statedb.Open(image)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { db.Close() })
					in.db = db
				}

				res, err := in.engine(in.rootID()).Sync(ctx, engine.Options{})
				if err != nil {
					t.Fatalf("cycle after the interrupted rebuild: %v", err)
				}
				if n := auditCount(res.Plan, reconcile.QuarantineLocal); n > 0 || len(res.Plan) != 0 || in.localFiles() != 10 || in.driveFiles() != 10 {
					t.Fatalf("nothing changed, yet the next cycle planned %d actions (%d local deletes); %d of 10 local files left, %d of 10 on Drive",
						len(res.Plan), n, in.localFiles(), in.driveFiles())
				}
			})
		}
	}
}
