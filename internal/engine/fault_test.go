package engine

// Fault tests F1–F3, F5 and scenario-level guard tests G1–G2
// (docs/testing.md). Crashes are simulated by executor.FaultHook aborting an
// op mid-flight: side effects up to the checkpoint remain, the DB commit
// never happens, and the next sync must repair. F4 (lost DB) lives in
// internal/doctor tests.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/executor"
)

func arm(t *testing.T, checkpointName string) {
	t.Helper()
	executor.FaultHook = func(cp string) error {
		if cp == checkpointName {
			return errors.New("injected crash at " + cp)
		}
		return nil
	}
	t.Cleanup(func() { executor.FaultHook = nil })
}

func disarm() { executor.FaultHook = nil }

// syncExpectFailure runs a sync expecting exactly one failed action.
func syncExpectFailure(t *testing.T, m *machine) {
	t.Helper()
	res, err := m.eng.Sync(context.Background(), Options{})
	if err != nil {
		t.Fatalf("[%s] sync errored instead of recording a failed op: %v", m.name, err)
	}
	if res.Failed != 1 {
		t.Fatalf("[%s] failed = %d, want exactly the injected failure: %v", m.name, res.Failed, res.Errors)
	}
}

// countByName returns how many non-trashed files with the name exist under
// the folder on the fake Drive.
func countByName(t *testing.T, fake *driveclient.Fake, folderID, name string) int {
	t.Helper()
	children, err := fake.List(context.Background(), folderID)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range children {
		if c.Name == name {
			n++
		}
	}
	return n
}

// --- F1: crash after upload, before DB commit ---------------------------

func TestF1CrashAfterUploadBeforeCommit(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)

	a.write(t, "f1.txt", "upload then crash")
	arm(t, executor.CPUploadBeforeCommit)
	syncExpectFailure(t, a)

	// The crash left the file on Drive but not in the baseline.
	if n := countByName(t, fake, root, "f1.txt"); n != 1 {
		t.Fatalf("after crash: %d copies on Drive, want 1", n)
	}
	items, _ := a.db.AllItems()
	if len(items) != 0 {
		t.Fatalf("after crash: %d baseline rows, want 0", len(items))
	}

	disarm()
	a.sync(t)

	// Repair must adopt, not re-upload: still exactly one copy.
	if n := countByName(t, fake, root, "f1.txt"); n != 1 {
		t.Errorf("after repair: %d copies on Drive, want 1 (no duplicate)", n)
	}
	items, _ = a.db.AllItems()
	if len(items) != 1 || items[0].RelPath != "f1.txt" {
		t.Errorf("after repair: baseline = %+v, want the adopted row", items)
	}
	res := a.sync(t)
	if len(res.Plan) != 0 {
		t.Errorf("post-repair sync planned %d actions, want steady state", len(res.Plan))
	}
}

// --- F2: crash mid-download -> temp cleaned, target never half-written ---

func TestF2CrashMidDownload(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)

	// Baseline file, then a remote edit to force a replacing download.
	a.write(t, "f2.txt", "old content")
	a.sync(t)
	var fileID string
	children, _ := fake.List(context.Background(), root)
	for _, c := range children {
		if c.Name == "f2.txt" {
			fileID = c.ID
		}
	}
	if _, err := fake.Update(context.Background(), fileID, strings.NewReader("new content, longer"), 19); err != nil {
		t.Fatal(err)
	}

	arm(t, executor.CPDownloadTempWritten)
	syncExpectFailure(t, a)

	// Target must be untouched: the old content, never a torn write.
	if got := a.read(t, "f2.txt"); got != "old content" {
		t.Fatalf("target after crash = %q, want untouched old content", got)
	}

	// Simulate what a hard kill would additionally leave behind: an orphan
	// temp file that in-process cleanup would have removed.
	orphan := filepath.Join(a.dir, ".synckeeper.tmp.crashleftover")
	if err := os.WriteFile(orphan, []byte("partial bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	disarm()
	a.sync(t)

	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Error("orphan temp file survived the repair run")
	}
	if got := a.read(t, "f2.txt"); got != "new content, longer" {
		t.Errorf("after retry, content = %q", got)
	}
	// The orphan temp was never uploaded either.
	if n := countByName(t, fake, root, ".synckeeper.tmp.crashleftover"); n != 0 {
		t.Error("orphan temp file was uploaded to Drive")
	}
}

// --- F3: crash between rename and DB commit ------------------------------

func TestF3CrashBetweenRenameAndCommit(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)

	a.write(t, "f3.txt", "version one")
	a.sync(t)
	var fileID string
	children, _ := fake.List(context.Background(), root)
	for _, c := range children {
		if c.Name == "f3.txt" {
			fileID = c.ID
		}
	}
	updated, err := fake.Update(context.Background(), fileID, strings.NewReader("version two!"), 12)
	if err != nil {
		t.Fatal(err)
	}

	arm(t, executor.CPDownloadBeforeCommit)
	syncExpectFailure(t, a)

	// Disk has the new content; the baseline still says version one.
	if got := a.read(t, "f3.txt"); got != "version two!" {
		t.Fatalf("target after crash = %q, want the renamed new content", got)
	}
	items, _ := a.db.AllItems()
	if len(items) != 1 || items[0].DriveVersion == updated.Version {
		t.Fatalf("baseline after crash = %+v, want the stale pre-download row", items)
	}

	disarm()
	a.sync(t)

	// Repair adopts (local md5 == remote md5): no transfer, no upload echo.
	f, _ := fake.Get(context.Background(), fileID)
	if f.Version != updated.Version {
		t.Errorf("drive version = %d, want untouched %d (repair must not re-upload)", f.Version, updated.Version)
	}
	items, _ = a.db.AllItems()
	if len(items) != 1 || items[0].DriveVersion != updated.Version {
		t.Errorf("baseline after repair = %+v, want the adopted row", items)
	}
	res := a.sync(t)
	if len(res.Plan) != 0 {
		t.Errorf("post-repair sync planned %d actions, want steady state", len(res.Plan))
	}
}

// --- F5: sync dir unmounted -> hard error, remote untouched --------------

func TestF5SyncDirUnmounted(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)

	a.write(t, "precious.txt", "do not lose")
	a.sync(t)

	// "Unmount": the sync dir vanishes entirely.
	if err := os.RemoveAll(a.dir); err != nil {
		t.Fatal(err)
	}
	if _, err := a.eng.Sync(context.Background(), Options{}); err == nil {
		t.Fatal("sync on a missing dir must hard-error")
	}
	if n := countByName(t, fake, root, "precious.txt"); n != 1 {
		t.Error("remote was touched despite the missing sync dir")
	}
}

// --- G1: mass delete blocked without --confirm-deletes -------------------

func TestG1MassDeleteBlocked(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)

	for i := 0; i < 12; i++ {
		a.write(t, fmt.Sprintf("bulk-%02d.txt", i), fmt.Sprintf("content %d", i))
	}
	a.sync(t)

	// Delete 11 of 12: over 25% and over 10 absolute.
	for i := 0; i < 11; i++ {
		a.remove(t, fmt.Sprintf("bulk-%02d.txt", i))
	}
	if _, err := a.eng.Sync(context.Background(), Options{}); err == nil {
		t.Fatal("mass delete must be blocked without confirmation")
	}
	children, _ := fake.List(context.Background(), root)
	if len(children) != 12 {
		t.Fatalf("remote has %d files after blocked sync, want all 12 untouched", len(children))
	}

	res, err := a.eng.Sync(context.Background(), Options{ConfirmDeletes: true})
	if err != nil || res.Failed > 0 {
		t.Fatalf("confirmed sync: err=%v failed=%d", err, res.Failed)
	}
	children, _ = fake.List(context.Background(), root)
	if len(children) != 1 {
		t.Errorf("remote has %d files after confirmed delete, want 1", len(children))
	}
}

// --- G3 (spec §8.1): the daemon defers a mass delete — it keeps syncing
// everything else and surfaces the block, instead of aborting the whole cycle.
func TestG3DaemonDefersMassDeleteButSyncsRest(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)

	for i := 0; i < 12; i++ {
		a.write(t, fmt.Sprintf("bulk-%02d.txt", i), fmt.Sprintf("content %d", i))
	}
	a.sync(t)

	// Delete 11 of 12 (over the threshold) and add an unrelated new file.
	for i := 0; i < 11; i++ {
		a.remove(t, fmt.Sprintf("bulk-%02d.txt", i))
	}
	a.write(t, "new.txt", "fresh content")

	res, err := a.eng.Sync(context.Background(), Options{DeferMassDelete: true})
	if err != nil {
		t.Fatalf("deferred mass delete must not error: %v", err)
	}
	if !res.GuardBlocked {
		t.Error("res.GuardBlocked = false, want the block surfaced")
	}
	if res.Failed != 0 {
		t.Fatalf("failed = %d, want 0: %v", res.Failed, res.Errors)
	}
	// The deletes were deferred: all 12 originals still on Drive, plus the new
	// upload that synced despite the block.
	children, _ := fake.List(context.Background(), root)
	if len(children) != 13 {
		t.Fatalf("remote has %d files, want 13 (12 originals kept + new upload)", len(children))
	}
	if countByName(t, fake, root, "new.txt") != 1 {
		t.Error("new.txt was not uploaded; the daemon must sync everything else")
	}

	// Confirming lets the deletes through on the next cycle.
	res, err = a.eng.Sync(context.Background(), Options{ConfirmDeletes: true})
	if err != nil || res.Failed > 0 {
		t.Fatalf("confirmed sync: err=%v failed=%d", err, res.Failed)
	}
	children, _ = fake.List(context.Background(), root)
	if len(children) != 2 {
		t.Errorf("remote has %d files after confirm, want 2 (bulk-11 + new.txt)", len(children))
	}
}

// --- G2: empty dir with populated DB -> hard error ------------------------

func TestG2EmptyDirBlocked(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)

	a.write(t, "data.txt", "tracked")
	a.sync(t)

	if err := os.RemoveAll(a.dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(a.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := a.eng.Sync(context.Background(), Options{}); err == nil {
		t.Fatal("empty dir with populated DB must hard-error")
	}
	if n := countByName(t, fake, root, "data.txt"); n != 1 {
		t.Error("remote file was trashed despite the guard")
	}
}

// --- Duplicate remote names: keep first by id, report the rest ------------

func TestDuplicateRemoteNames(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)

	ctx := context.Background()
	first, err := fake.Upload(ctx, root, "dup.txt", strings.NewReader("the winner"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fake.Upload(ctx, root, "dup.txt", strings.NewReader("the loser, different"), 20); err != nil {
		t.Fatal(err)
	}
	if _, err := fake.Upload(ctx, root, "dup.txt", strings.NewReader("another loser here!!"), 20); err != nil {
		t.Fatal(err)
	}

	res := a.sync(t)
	if got := a.read(t, "dup.txt"); got != "the winner" {
		t.Errorf("local content = %q, want the first-by-id copy", got)
	}
	dupSkips := 0
	for _, s := range res.Skips {
		if strings.Contains(s.Reason, "duplicate") {
			dupSkips++
		}
	}
	if dupSkips != 2 {
		t.Errorf("reported %d duplicate skips, want 2: %+v", dupSkips, res.Skips)
	}
	items, _ := a.db.AllItems()
	if len(items) != 1 || items[0].DriveFileID != first.ID {
		t.Errorf("baseline = %+v, want only the first-by-id file", items)
	}
}

// --- Quarantine retention --------------------------------------------------

func TestQuarantineRetentionPurge(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	a.eng.Cfg.Engine.QuarantineRetentionDays = 30

	old := filepath.Join(a.eng.QuarantineDir, "2020-01-01")
	recent := filepath.Join(a.eng.QuarantineDir, time.Now().Format("2006-01-02"))
	for _, dir := range []string{old, recent} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "rescued.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	a.write(t, "trigger.txt", "make the sync do something")
	a.sync(t)
	_ = fake
	_ = root

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("expired quarantine folder was not purged")
	}
	if _, err := os.Stat(filepath.Join(recent, "rescued.txt")); err != nil {
		t.Error("recent quarantine entry was purged too early")
	}
}

// --- Folder delete vs file-added-inside (delete-vs-edit at dir level) ------

func TestFolderDeleteVsAddInside(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)

	a.write(t, "proj/readme.md", "shared folder")
	a.write(t, "anchor.txt", "keeps a's dir non-empty")
	a.sync(t)
	b.sync(t)

	// a deletes the folder; b adds a file inside it, unsynced.
	a.remove(t, "proj")
	b.write(t, "proj/fresh.txt", "new work")
	a.sync(t) // folder + readme trashed remotely
	b.sync(t) // b must resurrect the folder for its new file
	a.sync(t) // a picks the resurrected folder back up
	b.sync(t)

	for _, m := range []*machine{a, b} {
		if got := m.read(t, "proj/fresh.txt"); got != "new work" {
			t.Errorf("[%s] fresh.txt = %q, want the new work to survive", m.name, got)
		}
		if m.exists("proj/readme.md") {
			t.Errorf("[%s] readme.md resurrected; the deleted, unedited file should stay deleted", m.name)
		}
	}
	assertConverged(t, a, b)
}
