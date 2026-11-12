package engine

// S035, engine level: the causes found minimizing the nine fuzz seeds of S035
// (testing.md S035.*), each on two machines with the real executor and the
// in-memory Drive fake.

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/executor"
)

// driveDuplicates lists every name held twice in one Drive folder under root —
// what §5's "first by id wins" then shadows on every machine.
func driveDuplicates(t *testing.T, fake *driveclient.Fake, rootID string) []string {
	t.Helper()
	var dups []string
	var walk func(parentID, prefix string)
	walk = func(parentID, prefix string) {
		children, err := fake.List(context.Background(), parentID)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, c := range children {
			rel := c.Name
			if prefix != "" {
				rel = prefix + "/" + c.Name
			}
			if seen[c.Name] {
				dups = append(dups, rel)
			}
			seen[c.Name] = true
			if c.IsDir() {
				walk(c.ID, rel)
			}
		}
	}
	walk(rootID, "")
	return dups
}

// S035.2 — another machine deletes folder D, then makes a new D with a file;
// this machine, which still has D (and an unchanged file in it), syncs both
// at once. It used to resurrect its old D on Drive beside the new one.
func TestS035FolderRecreatedElsewhereMakesNoSecondDriveFolder(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "D/x.txt", "x")
	settleAll(t, a, b)

	a.remove(t, "D")
	a.sync(t)
	a.write(t, "D/f.txt", "f")
	a.sync(t)

	b.sync(t)
	if dups := driveDuplicates(t, fake, root); len(dups) > 0 {
		t.Fatalf("Drive holds two items under one name: %v", dups)
	}
	settleAll(t, a, b)
	wantTrees(t, fake, root, map[string]string{"D/f.txt": "f"}, "", a, b)
	if dups := driveDuplicates(t, fake, root); len(dups) > 0 {
		t.Fatalf("Drive holds two items under one name: %v", dups)
	}
}

// S035.3 — Drive renamed folder D to N and a new folder took the name D;
// this machine created D/c.txt meanwhile. The plan moves D to N here, makes
// the new D, and uploads c.txt at N/c.txt — but the executor had seeded D's
// folder id with the NEW folder's before the move ran, and the move carried
// it to N: c.txt went up into the new D (fuzz seed 130: a duplicate name).
func TestS035UploadUnderAFolderMovedAwayLandsInTheMovedFolder(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "D/a.txt", "a")
	settleAll(t, a, b)

	a.rename(t, "D", "N")
	a.sync(t)
	a.write(t, "D/b.txt", "b")
	a.sync(t)

	b.write(t, "D/c.txt", "c")
	b.sync(t)
	if got := driveTreeOf(t, fake, root); got["N/c.txt"] != "c" || got["D/c.txt"] != "" {
		t.Fatalf("c.txt was uploaded into the wrong Drive folder: %v", got)
	}
	settleAll(t, a, b)
	wantTrees(t, fake, root, map[string]string{"N/a.txt": "a", "N/c.txt": "c", "D/b.txt": "b"}, "", a, b)
}

// failOnce makes the next checkpoint cp fail once — the commit's DB write
// failing after the disk half is done; the rest of the plan runs. A process
// death there leaves the same disk and journal, minus the rest of the cycle.
func failOnce(t *testing.T, cp string) {
	t.Helper()
	fired := false
	executor.FaultHook = func(name string) error {
		if name == cp && !fired {
			fired = true
			return errors.New("injected: commit lost at " + name)
		}
		return nil
	}
	t.Cleanup(func() { executor.FaultHook = nil })
}

func noFault() { executor.FaultHook = nil }

// syncFailing runs one cycle in which the injected failure must have fired
// (what depends on the failed action is refused with it).
func syncFailing(t *testing.T, m *machine) {
	t.Helper()
	res, err := m.eng.Sync(context.Background(), Options{})
	if err != nil {
		t.Fatalf("[%s] sync: %v", m.name, err)
	}
	if res.Failed == 0 {
		t.Fatalf("[%s] the injected failure did not fire: %v", m.name, res.Plan)
	}
}

// S035.4 — a folder rename applied from Drive, its commit lost; then a new
// folder takes the old name here with a file of the same name as one moved
// (fuzz seed 56). The old folder's rows met the new file at their old path:
// an update of the moved file's Drive item and an upload of the "new" file at
// the moved path — §4.5 refused the plan every cycle.
func TestS035FolderMoveCommitLostThenOldNameReused(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "D/f.txt", "orig")
	settleAll(t, a, b)

	a.rename(t, "D", "N")
	a.sync(t)
	failOnce(t, executor.CPMoveBeforeCommit)
	syncFailing(t, b)
	noFault()
	b.write(t, "D/f.txt", "new")
	b.sync(t)
	settleAll(t, a, b)
	wantTrees(t, fake, root, map[string]string{"N/f.txt": "orig", "D/f.txt": "new"}, "", a, b)
}

// S035.5 — the same lost commit, then another machine renames the folder
// again (fuzz seeds 66, 128): the moved folder's files read as new here and
// went up as copies beside Drive's, which came down too.
func TestS035FolderMoveCommitLostThenDriveMovesItAgain(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "D/f.txt", "orig")
	settleAll(t, a, b)

	a.rename(t, "D", "N")
	a.sync(t)
	failOnce(t, executor.CPMoveBeforeCommit)
	syncFailing(t, b)
	noFault()
	a.rename(t, "N", "M")
	a.sync(t)
	b.sync(t)
	settleAll(t, a, b)
	wantTrees(t, fake, root, map[string]string{"M/f.txt": "orig"}, "", a, b)
}

// S035.6 — the file form: a rename applied from Drive, its commit lost, then
// Drive renames the file again. The renamed file read as new: a duplicate.
func TestS035FileMoveCommitLostThenDriveMovesItAgain(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "f.txt", "orig")
	settleAll(t, a, b)

	a.rename(t, "f.txt", "g.txt")
	a.sync(t)
	failOnce(t, executor.CPMoveBeforeCommit)
	syncFailing(t, b)
	noFault()
	a.rename(t, "g.txt", "h.txt")
	a.sync(t)
	b.sync(t)
	settleAll(t, a, b)
	wantTrees(t, fake, root, map[string]string{"h.txt": "orig"}, "", a, b)
}

// S035.7 — a download into a folder Drive renamed, its commit lost, then a
// new folder takes the old name here (fuzz seed 52): the downloaded file read
// as new at the path its row's download targeted — download and upload on one
// path, refused every cycle. Drive renamed the folder, then the file in it;
// here the folder was renamed differently, so the folder move cannot run and
// the file comes down as a download into N. Needs S035.8 too: with the
// commit recovered, the folder's move onto N (now here) still never ran.
func TestS035DownloadCommitLostThenOldNameReused(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "D/f.txt", "orig")
	settleAll(t, a, b)

	a.rename(t, "D", "N")
	a.sync(t)
	a.rename(t, "N/f.txt", "N/g.txt")
	a.sync(t)
	b.rename(t, "D", "X")
	failOnce(t, executor.CPDownloadBeforeCommit)
	res, err := b.eng.Sync(context.Background(), Options{})
	if err != nil {
		t.Fatalf("[b] sync: %v", err)
	}
	if res.Failed == 0 {
		t.Fatalf("[b] the download's commit was meant to fail: %v", res.Plan)
	}
	noFault()
	b.write(t, "D/new.txt", "new")
	if _, err := b.eng.Sync(context.Background(), Options{}); err != nil {
		t.Fatalf("[b] sync after the lost download commit: %v", err)
	}
	settleAllLoose(t, a, b)
	if dups := driveDuplicates(t, fake, root); len(dups) > 0 {
		t.Fatalf("Drive holds two items under one name: %v", dups)
	}
	ta, tb := a.listTree(t), b.listTree(t)
	if ta["N/g.txt"] != "orig" || tb["N/g.txt"] != "orig" || ta["D/new.txt"] != "new" || tb["D/new.txt"] != "new" {
		t.Fatalf("trees: a=%v b=%v", ta, tb)
	}
	assertConverged(t, a, b)
}

// settleAllLoose is settleAll for shapes whose first cycles may legitimately
// fail an action (a folder move whose source is gone): it settles on zero
// actions and zero failures, refusing any hard error.
func settleAllLoose(t *testing.T, ms ...*machine) {
	t.Helper()
	for round := 0; round < 8; round++ {
		busy := 0
		for _, m := range ms {
			res, err := m.eng.Sync(context.Background(), Options{})
			if err != nil {
				t.Fatalf("[%s] sync: %v", m.name, err)
			}
			busy += len(res.Plan) + res.Failed
		}
		if busy == 0 {
			return
		}
	}
	t.Fatal("did not settle within 8 rounds")
}

// S035.8 — no crash: two machines rename one folder differently (Drive D→N
// and the file in it, here D→X), the file comes down into N (both kept), and
// a new folder D is made here. The
// folder's move D→N then met N already here: refused every cycle, and D's new
// file was uploaded at N/ where it is not — no convergence (the end state of
// fuzz seed 122; seed 52 after S035.7's fix).
func TestS035FolderMoveOntoAFolderAlreadyHereIsMerged(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "D/f.txt", "orig")
	settleAll(t, a, b)

	a.rename(t, "D", "N")
	a.sync(t)
	a.rename(t, "N/f.txt", "N/g.txt")
	a.sync(t)
	b.rename(t, "D", "X")
	if _, err := b.eng.Sync(context.Background(), Options{}); err != nil {
		t.Fatalf("[b] sync: %v", err)
	}
	b.write(t, "D/new.txt", "new")
	settleAllLoose(t, a, b)
	wantTrees(t, fake, root, map[string]string{"N/g.txt": "orig", "X/f.txt": "orig", "D/new.txt": "new"}, "", a, b)
	if dups := driveDuplicates(t, fake, root); len(dups) > 0 {
		t.Fatalf("Drive holds two items under one name: %v", dups)
	}
}

// S035.9 — no crash: Drive renames D to N while a new folder N, with a file,
// is made here; D is still here with its file. The move used to be refused
// every cycle. Merged: D's file moves into N beside the new one.
func TestS035FolderMoveOntoANewFolderHereIsMerged(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "D/f.txt", "orig")
	settleAll(t, a, b)

	a.rename(t, "D", "N")
	a.sync(t)
	b.write(t, "N/mine.txt", "mine")
	settleAllLoose(t, a, b)
	want := map[string]string{"N/f.txt": "orig", "N/mine.txt": "mine"}
	wantTrees(t, fake, root, want, "", a, b)
	if dups := driveDuplicates(t, fake, root); len(dups) > 0 {
		t.Fatalf("Drive holds two items under one name: %v", dups)
	}
}

// S035.11 — a swap made on Drive, cut here after its first rename (to the
// temporary conflict-copy name) and before that rename's commit, nothing
// edited: the commit is recovered and the swap completes — no copy left
// under the temporary name (it was kept and uploaded as a separate file).
func TestS035SwapCutAfterItsFirstRenameCompletes(t *testing.T) {
	fake, root, a, b, aID, bID := s031Swap(t)
	crashAtNth(t, a, executor.CPMoveBeforeCommit, 1)
	settle(t, a, b)
	wantTrees(t, fake, root, map[string]string{"a.txt": "content-B, longer", "b.txt": "content-A"}, "", a, b)
	wantSameItem(t, fake, root, "a.txt", bID)
	wantSameItem(t, fake, root, "b.txt", aID)
}

// localDirsOf is every folder under m's sync dir.
func localDirsOf(t *testing.T, m *machine) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	err := filepath.WalkDir(m.dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p != m.dir {
			rel, _ := filepath.Rel(m.dir, p)
			out[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// driveDirsOf is every folder under root on Drive.
func driveDirsOf(t *testing.T, fake *driveclient.Fake, rootID string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	var walk func(parentID, prefix string)
	walk = func(parentID, prefix string) {
		children, err := fake.List(context.Background(), parentID)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range children {
			if !c.IsDir() {
				continue
			}
			rel := c.Name
			if prefix != "" {
				rel = prefix + "/" + c.Name
			}
			out[rel] = true
			walk(c.ID, rel)
		}
	}
	walk(rootID, "")
	return out
}

// wantDirs: both machines and Drive hold exactly the folders in want.
func wantDirs(t *testing.T, fake *driveclient.Fake, root string, want []string, ms ...*machine) {
	t.Helper()
	w := map[string]bool{}
	for _, d := range want {
		w[d] = true
	}
	trees := map[string]map[string]bool{"drive": driveDirsOf(t, fake, root)}
	for _, m := range ms {
		trees[m.name] = localDirsOf(t, m)
	}
	for where, got := range trees {
		if !maps.Equal(got, w) {
			t.Errorf("[%s] folders %v, want %v", where, slices.Sorted(maps.Keys(got)), want)
		}
	}
}

// S035.15 — a Drive folder arrives at a path where this machine still has
// the folder of that name whose own Drive item is gone: another machine
// deleted D, then either renamed its synced folder E onto the name (by id, as
// the web UI does) or made a new D with a file. This machine's D holds x.txt
// unchanged, edited, or a new file n.txt beside it. The arriving folder takes
// the path; old D's rows meet §4.2 at their own paths — unchanged binned,
// edited resurrected into the arriving folder (edit beats delete), new
// uploaded into it — and nothing is named D twice on Drive. Every cycle runs
// without a failed action. Was (rule A of round 1, renamed-onto route):
// `move_local E → D` refused every cycle, and an edit went up into the
// trashed old D.
func TestS035FolderArrivesWhereTheDeletedOneStillIs(t *testing.T) {
	for _, route := range []string{"renamed onto", "made new"} {
		for _, here := range []string{"unchanged", "edited", "new file"} {
			t.Run(route+"/"+here, func(t *testing.T) {
				fake, root := newWorld(t)
				a := newMachine(t, "a", fake, root)
				b := newMachine(t, "b", fake, root)
				a.write(t, "D/x.txt", "x orig")
				a.write(t, "E/y.txt", "y orig, longer")
				settleAll(t, a, b)
				ctx := context.Background()
				dID, eID := remoteChildID(t, fake, root, "D"), remoteChildID(t, fake, root, "E")
				if err := fake.Trash(ctx, dID); err != nil {
					t.Fatal(err)
				}
				want := map[string]string{}
				dirs := []string{"D"}
				switch route {
				case "renamed onto":
					moveOnDrive(t, fake, driveMove{eID, root, "D"})
					want["D/y.txt"] = "y orig, longer"
				default:
					nd, err := fake.Mkdir(ctx, root, "D")
					if err != nil {
						t.Fatal(err)
					}
					if _, err := fake.Upload(ctx, nd.ID, "z.txt", strings.NewReader("z new"), 5); err != nil {
						t.Fatal(err)
					}
					want["D/z.txt"] = "z new"
					want["E/y.txt"] = "y orig, longer"
					dirs = append(dirs, "E")
				}
				switch here {
				case "edited":
					b.write(t, "D/x.txt", "x edited on b, not synced")
					want["D/x.txt"] = "x edited on b, not synced"
				case "new file":
					b.write(t, "D/n.txt", "n new on b")
					want["D/n.txt"] = "n new on b"
				}
				settleAll(t, b, a)
				wantTrees(t, fake, root, want, "", a, b)
				wantDirs(t, fake, root, dirs, a, b)
				if dups := driveDuplicates(t, fake, root); len(dups) > 0 {
					t.Fatalf("Drive holds two items under one name: %v", dups)
				}
			})
		}
	}
}

// S035.16 — finding 2 of round 1's review: the merge left the source folder
// empty, and it was created on Drive anyway — an empty D on every machine,
// also the one whose user never had it. S035.9's shape, folders compared.
func TestS035MergeLeavesNoEmptyFolderBehind(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "D/f.txt", "orig")
	settleAll(t, a, b)

	a.rename(t, "D", "N")
	a.sync(t)
	b.write(t, "N/mine.txt", "mine")
	settleAllLoose(t, a, b)
	wantTrees(t, fake, root, map[string]string{"N/f.txt": "orig", "N/mine.txt": "mine"}, "", a, b)
	wantDirs(t, fake, root, []string{"N"}, a, b)
}

// S035.17 — the merge's source still holds something new: it stays, a new
// folder on Drive. S035.8's shape, folders compared.
func TestS035MergeKeepsTheSourceFolderWithNewContent(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "D/f.txt", "orig")
	settleAll(t, a, b)

	a.rename(t, "D", "N")
	a.sync(t)
	a.rename(t, "N/f.txt", "N/g.txt")
	a.sync(t)
	b.rename(t, "D", "X")
	if _, err := b.eng.Sync(context.Background(), Options{}); err != nil {
		t.Fatalf("[b] sync: %v", err)
	}
	b.write(t, "D/new.txt", "new")
	settleAllLoose(t, a, b)
	wantTrees(t, fake, root, map[string]string{"N/g.txt": "orig", "X/f.txt": "orig", "D/new.txt": "new"}, "", a, b)
	wantDirs(t, fake, root, []string{"N", "X", "D"}, a, b)
}
