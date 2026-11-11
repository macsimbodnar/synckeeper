package engine

// Regression tests written by the 2026-11-11 adversarial audit
// (adocs/audit/2026-11-11_adversarial.md). Each one demonstrates an OPEN
// finding and therefore FAILS at the audited commit (1cea1b7). They are gated
// behind SYNCKEEPER_AUDIT_REPRO=1 so `make test` stays green until the fix for
// each lands; the fixing step removes the gate for its test, red first.
//
//	SYNCKEEPER_AUDIT_REPRO=1 go test ./internal/engine -run TestAudit20261111 -v
//
// The test names carry the finding numbers. F02 and F03 live in
// cmd/synckeeper/audit_2026_11_11_test.go because they replay `init`.

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

func auditRepro(t *testing.T) {
	t.Helper()
	if os.Getenv("SYNCKEEPER_AUDIT_REPRO") == "" {
		t.Skip("open audit finding; set SYNCKEEPER_AUDIT_REPRO=1 to reproduce (fails until fixed)")
	}
}

// hookClient wraps a Drive client so a test can stand in for the outside
// world acting at an exact moment: another machine's upload landing, or a
// program still writing the file being uploaded.
type hookClient struct {
	driveclient.Client
	mu          sync.Mutex
	onUpload    func(name string) // before the bytes are read
	afterUpload func(name string) // after Drive has stored them
	onUpdate    func(fileID string)
	afterUpdate func(fileID string) // after Drive has stored the new revision
}

func (h *hookClient) Upload(ctx context.Context, parentID, name string, content io.Reader, size int64) (driveclient.File, error) {
	h.mu.Lock()
	before, after := h.onUpload, h.afterUpload
	h.mu.Unlock()
	if before != nil {
		before(name)
	}
	f, err := h.Client.Upload(ctx, parentID, name, content, size)
	if after != nil {
		after(name)
	}
	return f, err
}

func (h *hookClient) Update(ctx context.Context, fileID string, content io.Reader, size int64) (driveclient.File, error) {
	h.mu.Lock()
	hook, after := h.onUpdate, h.afterUpdate
	h.mu.Unlock()
	if hook != nil {
		hook(fileID)
	}
	f, err := h.Client.Update(ctx, fileID, content, size)
	if after != nil {
		after(fileID)
	}
	return f, err
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

// auditDriveFiles counts the non-trashed files under a Drive folder.
func auditDriveFiles(t *testing.T, fake *driveclient.Fake, rootID string) int {
	t.Helper()
	n := 0
	queue := []string{rootID}
	for len(queue) > 0 {
		kids, err := fake.List(context.Background(), queue[0])
		if err != nil {
			t.Fatal(err)
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

// F01 — A sync_dir that is a symlink to a directory empties the Drive folder.
//
// scanner.Scan walks with filepath.WalkDir, which does not follow a symlink
// given as its root: the walk visits the link itself and stops, with no error.
// guards.EnsureSyncDir uses os.Stat/os.ReadDir, which DO follow it, so the
// sanity guard is satisfied. The scan therefore reports an empty tree beside a
// full baseline, every tracked file reads as "deleted locally", and — remote
// deletions being unguarded by design (W14) — the whole Drive folder goes to
// Drive's bin. Every other machine then moves its own copy to its system bin.
func TestAudit20261111_F01_SymlinkedSyncDirEmptiesDrive(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)

	b.write(t, "one.txt", "1")
	b.write(t, "docs/two.txt", "2")
	b.sync(t)
	if got := auditDriveFiles(t, fake, root); got != 2 {
		t.Fatalf("setup: want 2 files on Drive, have %d", got)
	}

	// Machine a keeps its data on another disk and points sync_dir at it
	// through a symlink (~/Synckeeper -> /mnt/data/Synckeeper).
	realDir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "Synckeeper")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	a.dir, a.eng.SyncDir = link, link

	// Cycle 1 is `init`'s merge, cycle 2 the daemon's first cycle.
	trashed := 0
	for cycle := 1; cycle <= 2; cycle++ {
		res, err := a.eng.Sync(context.Background(), Options{})
		if err != nil {
			t.Fatalf("cycle %d: %v", cycle, err)
		}
		trashed += auditCount(res.Plan, reconcile.TrashRemote)
	}
	b.sync(t)

	if left := auditDriveFiles(t, fake, root); left != 2 || trashed > 0 || len(b.listTree(t)) != 2 {
		t.Fatalf("a symlinked sync dir was scanned as empty and read as a deletion: "+
			"%d trash_remote actions, %d of 2 files left in the Drive folder, %d of 2 files left on machine b; "+
			"nobody deleted anything",
			trashed, left, len(b.listTree(t)))
	}
}

// F04 — Uploading a local edit overwrites a newer remote edit that landed
// after this cycle refreshed its view of Drive: last writer wins, no conflict
// copy, and the other machine's content is then replaced on its own disk too.
//
// The local-write gate (spec §7) re-checks every LOCAL path immediately before
// mutating it. Nothing re-checks the REMOTE side: Update(fileID) replaces
// whatever Drive holds at that moment.
func TestAudit20261111_F04_UpdateRemoteOverwritesConcurrentRemoteEdit(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)

	a.write(t, "notes.txt", "v1")
	a.sync(t)
	b.sync(t)

	// Both machines edit the same file.
	a.write(t, "notes.txt", "edit made on A")
	b.write(t, "notes.txt", "edit made on B, longer")

	// A's cycle has already refreshed its view of Drive and is about to send
	// its upload when B's upload lands. In the field this window is as long as
	// A's scan plus its transfer queue: seconds normally, hours behind a large
	// backlog.
	raced := false
	a.eng.Client = &hookClient{Client: fake, onUpdate: func(string) {
		if raced {
			return
		}
		raced = true
		b.sync(t)
	}}
	if _, err := a.eng.Sync(context.Background(), Options{}); err != nil {
		t.Fatalf("A sync: %v", err)
	}
	a.eng.Client = fake

	for i := 0; i < 3; i++ {
		a.sync(t)
		b.sync(t)
	}

	// Two different edits were made. Invariant 5: both survive, one of them as
	// a conflict copy, on both machines.
	for _, m := range []*machine{a, b} {
		tree := m.listTree(t)
		var all []string
		for _, c := range tree {
			all = append(all, c)
		}
		joined := strings.Join(all, "\n")
		if !strings.Contains(joined, "edit made on A") || !strings.Contains(joined, "edit made on B, longer") {
			t.Errorf("[%s] one of two concurrent edits is gone with no conflict copy: files on disk = %v", m.name, tree)
		}
	}
}

// F05 — A file that is still being written while it uploads ends up truncated
// under its real name, on every machine.
//
// The executor hashes the file, then streams it. A writer that is still
// appending (a large copy into the sync folder, a browser download, an
// export) makes the two disagree: Drive stores whatever was on disk when the
// stream hit EOF, a prefix of the final file. The md5 check notices and
// commits nothing, but the truncated item stays on Drive. The next cycle sees
// "new here, new there, different content" and resolves it as a conflict in
// Drive's favour: the truncated upload takes the canonical name and the
// complete file is renamed to a conflict copy.
func TestAudit20261111_F05_FileWrittenDuringUploadIsTruncatedAtCanonicalName(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)

	const part1, part2, part3 = "PART-1|", "PART-2|", "PART-3"
	target := filepath.Join(a.dir, "video.mkv")
	appendTo := func(s string) {
		f, err := os.OpenFile(target, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Errorf("append: %v", err)
			return
		}
		f.WriteString(s)
		f.Close()
	}

	// A copy into the sync folder is in progress. One third is on disk when
	// the cycle scans and hashes it...
	a.write(t, "video.mkv", part1)

	// ...the second third lands between the executor's hash and the moment the
	// upload stream reads the file, and the last third after the stream has
	// already hit EOF. Nobody edits anything; the copy simply finishes.
	done := false
	a.eng.Client = &hookClient{Client: fake,
		onUpload: func(name string) {
			if name == "video.mkv" && !done {
				appendTo(part2)
			}
		},
		afterUpload: func(name string) {
			if name == "video.mkv" && !done {
				done = true
				appendTo(part3)
			}
		},
	}
	if _, err := a.eng.Sync(context.Background(), Options{}); err != nil {
		t.Fatalf("cycle 1: %v", err)
	}
	// The copy is complete. Let both machines settle.
	for i := 0; i < 4; i++ {
		if _, err := a.eng.Sync(context.Background(), Options{}); err != nil {
			t.Fatalf("settle cycle %d: %v", i, err)
		}
		if _, err := b.eng.Sync(context.Background(), Options{}); err != nil {
			t.Fatalf("settle cycle %d (b): %v", i, err)
		}
	}

	const whole = part1 + part2 + part3
	for _, m := range []*machine{a, b} {
		tree := m.listTree(t)
		if len(tree) != 1 || tree["video.mkv"] != whole {
			t.Errorf("[%s] one file was copied into the sync folder and nobody edited it anywhere; want exactly video.mkv = %q, have %d file(s): %v",
				m.name, whole, len(tree), tree)
		}
	}
}

// F07 — A tracked folder replaced by a symlink is trashed on Drive.
//
// Relocating a big subfolder to another disk and leaving a symlink in its
// place is an ordinary thing to do. The scanner reports the symlink as a skip
// and leaves the path out of the snapshot; unlike an unreadable directory
// (W18-G), nothing holds the baseline rows beneath it harmless, so the whole
// subtree reads as deleted locally and its Drive folder goes to Drive's bin —
// and from there off every other machine. Under the daemon the skip is not
// reported anywhere (S003).
func TestAudit20261111_F07_FolderReplacedBySymlinkIsTrashedOnDrive(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "photos/1.jpg", "one")
	a.write(t, "photos/2.jpg", "two")
	a.write(t, "keep.txt", "k")
	a.sync(t)
	b.sync(t)

	elsewhere := filepath.Join(t.TempDir(), "photos")
	if err := os.Rename(filepath.Join(a.dir, "photos"), elsewhere); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(a.dir, "photos")); err != nil {
		t.Fatal(err)
	}
	res, err := a.eng.Sync(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	b.sync(t)

	if n := auditCount(res.Plan, reconcile.TrashRemote); n > 0 || auditDriveFiles(t, fake, root) != 3 || len(b.listTree(t)) != 3 {
		t.Fatalf("a folder replaced by a symlink to itself was read as deleted: %d trash_remote actions, "+
			"%d of 3 files left on Drive, %d of 3 left on machine b (skips reported: %v)",
			n, auditDriveFiles(t, fake, root), len(b.listTree(t)), res.Skips)
	}
	reported := false
	for _, s := range res.Skips {
		if s.RelPath == "photos" {
			reported = true
		}
	}
	if !reported {
		t.Fatalf("the symlink was not reported as a skip: %v", res.Skips)
	}
}

// F08 — One unreadable FILE stops all syncing, every cycle, for good.
//
// W18-G made an unreadable DIRECTORY a reported skip. A file the scan cannot
// open to hash still fails scanner.Scan, and with it the whole cycle — the
// same wedge, one level down.
func TestAudit20261111_F08_OneUnreadableFileWedgesEveryCycle(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)

	a.write(t, "ok.txt", "fine")
	a.write(t, "locked.txt", "cannot be read")
	locked := filepath.Join(a.dir, "locked.txt")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o644) })

	for cycle := 1; cycle <= 3; cycle++ {
		if _, err := a.eng.Sync(context.Background(), Options{}); err != nil {
			t.Fatalf("cycle %d: one unreadable file failed the whole cycle, so ok.txt never syncs: %v", cycle, err)
		}
	}
	if auditDriveFiles(t, fake, root) == 0 {
		t.Fatalf("ok.txt never reached Drive")
	}
}

// F10 — The Drive folder being in Drive's bin goes unnoticed.
//
// Spec §12: a root that is "genuinely gone (404, or in Drive's bin) is
// recreated". Only `init` and `doctor --repair` call root.Resolve; `watch`
// and `sync` read root_folder_id from meta and never look at the folder
// again. Against the fake, a cycle after the root is binned reports clean
// success while uploading into the binned folder.
//
// What this test cannot show is what real Drive's change feed does for the
// folder's descendants on trash and on purge — see the report.
func TestAudit20261111_F10_BinnedDriveRootIsSyncedIntoSilently(t *testing.T) {
	auditRepro(t)
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	a.write(t, "one.txt", "1")
	a.sync(t)
	a.sync(t)

	if err := fake.Trash(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	a.write(t, "two.txt", "2")
	res, err := a.eng.Sync(context.Background(), Options{})

	f, gerr := fake.Get(context.Background(), a.eng.RootID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if err == nil && res.Failed == 0 && len(res.Skips) == 0 && f.Trashed {
		t.Fatalf("the Drive folder is in Drive's bin and the cycle reported clean success: "+
			"%d actions executed into the binned folder, 0 failed, 0 skips, no error", res.Executed)
	}
}
