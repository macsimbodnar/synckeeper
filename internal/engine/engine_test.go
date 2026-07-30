package engine

// Scenario tests S1–S8 (docs/testing.md): N simulated machines, each with
// its own sync dir and state DB, against one shared fake Drive.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/config"
	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
	"github.com/macsimbodnar/synckeeper/internal/trash"
)

type machine struct {
	name string
	dir  string
	db   *statedb.DB
	eng  *Engine
	bin  *trash.Fake // this machine's system bin (W13)
}

func newWorld(t *testing.T) (*driveclient.Fake, string) {
	t.Helper()
	fake := driveclient.NewFake()
	folder, err := fake.Mkdir(context.Background(), driveclient.FakeRootID, "Synckeeper")
	if err != nil {
		t.Fatal(err)
	}
	return fake, folder.ID
}

func newMachine(t *testing.T, name string, fake *driveclient.Fake, rootID string) *machine {
	t.Helper()
	base := t.TempDir()
	dir := filepath.Join(base, "sync")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := statedb.Open(filepath.Join(base, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	token, err := fake.StartPageToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetMeta(statedb.MetaPageToken, token); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Engine.MachineName = name
	bin := trash.NewFake(filepath.Join(base, "bin"))
	return &machine{
		name: name,
		dir:  dir,
		db:   db,
		bin:  bin,
		eng: &Engine{
			DB: db, Client: fake, Cfg: cfg, SyncDir: dir,
			QuarantineDir: filepath.Join(base, "quarantine"), RootID: rootID,
			Trash: bin,
		},
	}
}

// binPath is where a trashed rel_path lands in this machine's bin: the item
// keeps its base name, since the bin is flat like the real ones.
func (m *machine) binPath(rel string) string {
	return filepath.Join(m.bin.Dir, filepath.Base(rel))
}

// binHas reports whether the bin holds the item at the given path inside a
// trashed entry (e.g. "docs" trashed whole still holds "docs/a.txt").
func (m *machine) binRead(t *testing.T, entry, sub string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(m.bin.Dir, entry, filepath.FromSlash(sub)))
	if err != nil {
		return ""
	}
	return string(raw)
}

func (m *machine) sync(t *testing.T) *Result {
	t.Helper()
	res := m.syncRaw(t)
	// W16-E4: a clean cycle leaves the two sides of the state DB agreeing.
	// Checked here rather than per test so every scenario in the package is
	// a witness — the defect's general form is "a write path that updates
	// the baseline without the mirror", and any of them could grow one.
	assertMirrorCoversBaseline(t, m)
	return res
}

// syncRaw is one clean cycle without the E4 invariant check — for the W16
// tests, which assert the invariant where they mean to and would otherwise
// have their behavioural assertions shadowed by it.
func (m *machine) syncRaw(t *testing.T) *Result {
	t.Helper()
	res, err := m.eng.Sync(context.Background(), Options{})
	if err != nil {
		t.Fatalf("[%s] sync: %v", m.name, err)
	}
	if res.Failed > 0 {
		t.Fatalf("[%s] sync had %d failed actions: %v", m.name, res.Failed, res.Errors)
	}
	return res
}

func (m *machine) write(t *testing.T, rel, content string) {
	t.Helper()
	p := filepath.Join(m.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	// Nudge mtime so successive writes in one test never collide.
	now := time.Now()
	os.Chtimes(p, now, now.Add(time.Duration(len(content))*time.Millisecond))
}

func (m *machine) read(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(m.dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("[%s] read %s: %v", m.name, rel, err)
	}
	return string(raw)
}

func (m *machine) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(m.dir, filepath.FromSlash(rel)))
	return err == nil
}

func (m *machine) remove(t *testing.T, rel string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(m.dir, filepath.FromSlash(rel))); err != nil {
		t.Fatal(err)
	}
}

func (m *machine) rename(t *testing.T, from, to string) {
	t.Helper()
	src := filepath.Join(m.dir, filepath.FromSlash(from))
	dst := filepath.Join(m.dir, filepath.FromSlash(to))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(src, dst); err != nil {
		t.Fatal(err)
	}
}

// listTree returns all files (not dirs) under the machine's sync dir.
func (m *machine) listTree(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(m.dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(m.dir, p)
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

func assertConverged(t *testing.T, machines ...*machine) {
	t.Helper()
	ref := machines[0].listTree(t)
	for _, m := range machines[1:] {
		got := m.listTree(t)
		if len(got) != len(ref) {
			t.Fatalf("trees diverge: %s has %d files, %s has %d\n%s: %v\n%s: %v",
				machines[0].name, len(ref), m.name, len(got), machines[0].name, keys(ref), m.name, keys(got))
		}
		for p, content := range ref {
			if got[p] != content {
				t.Errorf("trees diverge at %s: %s=%q %s=%q", p, machines[0].name, content, m.name, got[p])
			}
		}
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// --- S1: create local -> appears remote and on machine B --------------

func TestS1CreateLocalPropagates(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)

	a.write(t, "hello.txt", "hello world")
	a.sync(t)
	b.sync(t)

	if got := b.read(t, "hello.txt"); got != "hello world" {
		t.Errorf("machine b content = %q", got)
	}
	assertConverged(t, a, b)
}

// --- S2: create remote -> appears local -------------------------------

func TestS2CreateRemotePropagates(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)

	if _, err := fake.Upload(context.Background(), root, "remote.txt", strings.NewReader("from drive"), 10); err != nil {
		t.Fatal(err)
	}
	a.sync(t)
	if got := a.read(t, "remote.txt"); got != "from drive" {
		t.Errorf("content = %q", got)
	}
}

// --- S3: sequential edits both directions ------------------------------

func TestS3SequentialEdits(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)

	a.write(t, "doc.txt", "v1")
	a.sync(t)
	b.sync(t)

	b.write(t, "doc.txt", "v2 from b")
	b.sync(t)
	a.sync(t)
	if got := a.read(t, "doc.txt"); got != "v2 from b" {
		t.Fatalf("a sees %q after b's edit", got)
	}

	a.write(t, "doc.txt", "v3 from a!")
	a.sync(t)
	b.sync(t)
	if got := b.read(t, "doc.txt"); got != "v3 from a!" {
		t.Fatalf("b sees %q after a's edit", got)
	}
	assertConverged(t, a, b)
}

// --- S4: concurrent divergent edit -> conflict copy everywhere ---------

func TestS4ConcurrentEditConflict(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)

	a.write(t, "notes.md", "base")
	a.sync(t)
	b.sync(t)

	a.write(t, "notes.md", "edit from a")
	b.write(t, "notes.md", "edit from b!!")
	a.sync(t) // a uploads its revision first
	b.sync(t) // b hits the conflict: local becomes the copy, remote wins
	a.sync(t) // a picks up b's conflict copy

	aTree := a.listTree(t)
	if aTree["notes.md"] != "edit from a" {
		t.Errorf("canonical notes.md = %q, want a's edit (it reached Drive first)", aTree["notes.md"])
	}
	var conflictPath string
	for p := range aTree {
		if strings.Contains(p, "(conflict b ") {
			conflictPath = p
		}
	}
	if conflictPath == "" {
		t.Fatalf("no conflict copy found; tree: %v", keys(aTree))
	}
	if aTree[conflictPath] != "edit from b!!" {
		t.Errorf("conflict copy = %q, want b's edit", aTree[conflictPath])
	}
	assertConverged(t, a, b)
}

// --- S5: deletes propagate as trash / quarantine ------------------------

func TestS5DeletePropagates(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)

	a.write(t, "gone.txt", "delete me")
	a.write(t, "stay.txt", "keep me!")
	a.sync(t)
	b.sync(t)

	// Local delete on a -> trashed on Drive, quarantined on b.
	a.remove(t, "gone.txt")
	a.sync(t)
	b.sync(t)
	if b.exists("gone.txt") {
		t.Error("gone.txt still on machine b")
	}
	ctx := context.Background()
	children, _ := fake.List(ctx, root)
	for _, c := range children {
		if c.Name == "gone.txt" {
			t.Error("gone.txt still listed (not trashed) on Drive")
		}
	}
	// b's copy is preserved in b's system bin, not destroyed (W13).
	if raw, err := os.ReadFile(b.binPath("gone.txt")); err != nil || string(raw) != "delete me" {
		t.Errorf("gone.txt not recoverable from machine b's bin: %q, %v", raw, err)
	}
	assertConverged(t, a, b)
}

// --- S6: delete vs edit, both orders -> edit survives -------------------

func TestS6EditBeatsDelete(t *testing.T) {
	for _, order := range []string{"delete_first", "edit_first"} {
		t.Run(order, func(t *testing.T) {
			fake, root := newWorld(t)
			a := newMachine(t, "a", fake, root)
			b := newMachine(t, "b", fake, root)

			a.write(t, "fight.txt", "original")
			// A second file keeps a's dir non-empty after the delete, so the
			// empty-dir-with-populated-DB guard (G2) does not trip.
			a.write(t, "keeper.txt", "stays")
			a.sync(t)
			b.sync(t)

			a.remove(t, "fight.txt")
			b.write(t, "fight.txt", "edited on b")
			if order == "delete_first" {
				a.sync(t)
				b.sync(t)
			} else {
				b.sync(t)
				a.sync(t)
			}
			a.sync(t)
			b.sync(t)

			if got := a.read(t, "fight.txt"); got != "edited on b" {
				t.Errorf("a sees %q, want the edit to survive", got)
			}
			assertConverged(t, a, b)
		})
	}
}

// --- S7: renames and moves ----------------------------------------------

func TestS7LocalRenameBecomesRemoteMove(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)

	a.write(t, "old-name.txt", "contents stay identical")
	a.sync(t)
	b.sync(t)

	ctx := context.Background()
	children, _ := fake.List(ctx, root)
	var originalID string
	for _, c := range children {
		if c.Name == "old-name.txt" {
			originalID = c.ID
		}
	}

	a.rename(t, "old-name.txt", "new-name.txt")
	a.sync(t)

	// Same Drive file id -> it was a move, not delete + re-upload.
	f, err := fake.Get(ctx, originalID)
	if err != nil {
		t.Fatal(err)
	}
	if f.Trashed || f.Name != "new-name.txt" {
		t.Errorf("drive file after rename: name=%q trashed=%v, want moved in place", f.Name, f.Trashed)
	}

	b.sync(t)
	if !b.exists("new-name.txt") || b.exists("old-name.txt") {
		t.Error("rename did not propagate to machine b")
	}
	assertConverged(t, a, b)
}

func TestS7RemoteMoveAppliesLocally(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)

	a.write(t, "dir1/file.txt", "moving content")
	a.write(t, "dir2/.keepme", "x")
	a.sync(t)

	ctx := context.Background()
	// Find ids for dir2 and the file, then move file into dir2 on Drive.
	var fileID, dir2ID string
	children, _ := fake.List(ctx, root)
	for _, c := range children {
		if c.Name == "dir2" {
			dir2ID = c.ID
		}
		if c.Name == "dir1" {
			sub, _ := fake.List(ctx, c.ID)
			for _, s := range sub {
				if s.Name == "file.txt" {
					fileID = s.ID
				}
			}
		}
	}
	if _, err := fake.Move(ctx, fileID, dir2ID, "renamed.txt"); err != nil {
		t.Fatal(err)
	}

	a.sync(t)
	if !a.exists("dir2/renamed.txt") || a.exists("dir1/file.txt") {
		t.Errorf("remote move not applied locally; tree: %v", keys(a.listTree(t)))
	}
	if got := a.read(t, "dir2/renamed.txt"); got != "moving content" {
		t.Errorf("moved content = %q", got)
	}
}

func TestS7MoveOutOfTreeQuarantines(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)

	a.write(t, "escaping.txt", "bye")
	a.sync(t)

	ctx := context.Background()
	children, _ := fake.List(ctx, root)
	var fileID string
	for _, c := range children {
		if c.Name == "escaping.txt" {
			fileID = c.ID
		}
	}
	// Move to Drive root: outside the synced tree.
	if _, err := fake.Move(ctx, fileID, driveclient.FakeRootID, "escaping.txt"); err != nil {
		t.Fatal(err)
	}

	a.sync(t)
	if a.exists("escaping.txt") {
		t.Error("file moved out of tree still present locally")
	}
	// Preserved in the system bin, not destroyed.
	if raw, err := os.ReadFile(a.binPath("escaping.txt")); err != nil || string(raw) != "bye" {
		t.Errorf("out-of-tree file not recoverable from the bin: %q, %v", raw, err)
	}
}

// --- S8: nested folders, deep tree, empty folders ------------------------

func TestS8NestedTree(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)

	deep := "l1/l2/l3/l4/l5"
	a.write(t, deep+"/deep.txt", "deep content")
	a.write(t, "l1/mid.txt", "mid")
	if err := os.MkdirAll(filepath.Join(a.dir, "empty", "nested-empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	a.sync(t)
	b.sync(t)

	if got := b.read(t, deep+"/deep.txt"); got != "deep content" {
		t.Errorf("deep file = %q", got)
	}
	if !b.exists("empty/nested-empty") {
		t.Error("empty folders were not propagated")
	}

	// Delete the whole deep tree on b; a follows.
	b.remove(t, "l1")
	b.sync(t)
	a.sync(t)
	if a.exists("l1") {
		t.Errorf("deleted tree still present on a; tree: %v", keys(a.listTree(t)))
	}
	if !a.exists("empty/nested-empty") {
		t.Error("unrelated empty folder disappeared")
	}
	assertConverged(t, a, b)
}

// --- Idempotence: a second sync with no changes does nothing -------------

func TestSecondSyncIsNoop(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)

	a.write(t, "x/y.txt", "steady state")
	a.sync(t)

	res := a.sync(t)
	if len(res.Plan) != 0 {
		t.Errorf("second sync planned %d actions, want 0: %+v", len(res.Plan), res.Plan)
	}
}

// --- Ignore patterns are not synced --------------------------------------

func TestIgnoredFilesStayLocal(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)

	a.write(t, ".DS_Store", "junk")
	a.write(t, "real.txt", "real")
	a.sync(t)

	children, _ := fake.List(context.Background(), root)
	for _, c := range children {
		if c.Name == ".DS_Store" {
			t.Error(".DS_Store was uploaded")
		}
	}
	if fmt.Sprint(children) == "" || !a.exists(".DS_Store") {
		t.Error("ignored file should remain local")
	}
}
