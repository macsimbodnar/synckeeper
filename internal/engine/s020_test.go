package engine

// S020 (DEC-006), engine level: a folder deleted on one machine while another
// renames or moves it on Drive. The rename wins: the deleting machine gets the
// folder back under its new name, nothing goes to Drive's bin, and no
// deletion is counted or reported (testing.md S020.*).

import (
	"context"
	"errors"
	"maps"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/executor"
	"github.com/macsimbodnar/synckeeper/internal/names"
)

// s020Rows is the machine's baseline as rel_path → is-dir.
func s020Rows(t *testing.T, m *machine) map[string]bool {
	t.Helper()
	items, err := m.db.AllItems()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, it := range items {
		out[it.RelPath] = it.IsDir
	}
	return out
}

// s020NoDeletion: the cycle deleted nothing, on either side, and failed nothing.
func s020NoDeletion(t *testing.T, m *machine, res *Result) {
	t.Helper()
	if res.Failed != 0 || res.DeletedRemote != 0 || res.DeletedLocal != 0 || res.LargeDeletion {
		t.Fatalf("[%s] Failed=%d DeletedRemote=%d DeletedLocal=%d LargeDeletion=%v, want none\nplan: %+v",
			m.name, res.Failed, res.DeletedRemote, res.DeletedLocal, res.LargeDeletion, res.Plan)
	}
	for _, a := range res.Plan {
		switch a.Type {
		case "trash_remote", "quarantine_local":
			t.Fatalf("[%s] planned %s %s: the rename must win, nothing deleted\nplan: %+v",
				m.name, a.Type, a.RelPath, res.Plan)
		}
	}
}

// S020.1 — the shape of the step: D/x.txt, D/y.txt synced on A and B; A
// deletes D; B renames D to E. Was: A planned move_local D→E (failed: no such
// directory), trashed D/x.txt, D/y.txt and E as three Drive-bin entries, and B
// then binned E — the rename discarded. Now A downloads E back in one cycle.
func TestS020FolderDeletedHereRenamedOnDriveComesBack(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "D/x.txt", "x")
	a.write(t, "D/y.txt", "y")
	settleAll(t, a, b)
	folderID := driveIDOf(t, a, "D")
	trashed := fake.TrashCount()

	a.remove(t, "D")
	b.rename(t, "D", "E")
	b.sync(t)

	res := a.sync(t)
	s020NoDeletion(t, a, res)
	if got := fake.TrashCount(); got != trashed {
		t.Fatalf("Drive's bin gained %d entries, want none", got-trashed)
	}
	want := map[string]string{"E/x.txt": "x", "E/y.txt": "y"}
	wantTrees(t, fake, root, want, "", a, b)
	if got := slices.Sorted(maps.Keys(s020Rows(t, a))); !slices.Equal(got, []string{"E", "E/x.txt", "E/y.txt"}) {
		t.Fatalf("[a] baseline rows = %v, want E, E/x.txt, E/y.txt", got)
	}
	if id := driveIDOf(t, a, "E"); id != folderID {
		t.Fatalf("[a] E's row holds Drive id %s, want the renamed folder's %s", id, folderID)
	}
	// Converged in that one cycle; B untouched.
	if p := a.sync(t).Plan; len(p) != 0 {
		t.Fatalf("[a] second cycle planned %+v, want nothing", p)
	}
	if p := b.sync(t).Plan; len(p) != 0 {
		t.Fatalf("[b] planned %+v, want nothing", p)
	}
	if got := fake.TrashCount(); got != trashed {
		t.Fatalf("Drive's bin gained %d entries, want none", got-trashed)
	}
	wantTrees(t, fake, root, want, "", a, b)
}

// S020.2 — Drive moved D under another folder rather than renamed it, and
// changed its content meanwhile: x edited, z added, y deleted. Every change
// is Drive's; A's deletion of D yields to all of them (§4.2 per file: edited
// or moved → download, deleted on both sides → forgotten).
func TestS020FolderDeletedHereMovedAndChangedOnDriveComesBack(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "D/x.txt", "x")
	a.write(t, "D/y.txt", "y")
	a.write(t, "P/p.txt", "p")
	settleAll(t, a, b)
	trashed := fake.TrashCount()

	a.remove(t, "D")
	b.rename(t, "D", "P/E")
	b.sync(t)
	b.write(t, "P/E/x.txt", "x2")
	b.write(t, "P/E/z.txt", "z")
	b.remove(t, "P/E/y.txt")
	b.sync(t)
	trashedByB := fake.TrashCount() - trashed // y.txt, B's own deletion

	res := a.sync(t)
	s020NoDeletion(t, a, res)
	if got := fake.TrashCount() - trashed; got != trashedByB {
		t.Fatalf("Drive's bin gained %d entries from A, want none", got-trashedByB)
	}
	want := map[string]string{"P/p.txt": "p", "P/E/x.txt": "x2", "P/E/z.txt": "z"}
	settleAll(t, a, b)
	wantTrees(t, fake, root, want, "", a, b)
}

// S020.3 — only D/S deleted here, D renamed to E on Drive. S itself was not
// renamed (§4.3: its new path is its parent's change), so its deletion
// stands: S goes to Drive's bin as ONE entry with its files (§4.2's folder
// collapse). Was: trashed as three entries — the files at their old paths
// escaped the collapse — so restoring S from the bin brought back an empty
// folder.
func TestS020SubfolderDeletedHereParentRenamedOnDriveIsOneBinEntry(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "D/a.txt", "a")
	a.write(t, "D/S/x.txt", "x")
	a.write(t, "D/S/y.txt", "y")
	settleAll(t, a, b)
	trashed := fake.TrashCount()

	a.remove(t, "D/S")
	b.rename(t, "D", "E")
	b.sync(t)

	a.sync(t)
	if got := fake.TrashCount() - trashed; got != 1 {
		t.Fatalf("Drive's bin gained %d entries, want 1 (S with its files)", got)
	}
	settleAll(t, a, b)
	wantTrees(t, fake, root, map[string]string{"E/a.txt": "a"}, "", a, b)
}

// S020.4 — D/S deleted here, Drive renamed D/S to D/T: the S020 shape one
// level down — T comes back here with its files; D/a.txt untouched.
func TestS020SubfolderDeletedHereRenamedOnDriveComesBack(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "D/a.txt", "a")
	a.write(t, "D/S/x.txt", "x")
	settleAll(t, a, b)
	trashed := fake.TrashCount()

	a.remove(t, "D/S")
	b.rename(t, "D/S", "D/T")
	b.sync(t)

	s020NoDeletion(t, a, a.sync(t))
	if got := fake.TrashCount(); got != trashed {
		t.Fatalf("Drive's bin gained %d entries, want none", got-trashed)
	}
	settleAll(t, a, b)
	wantTrees(t, fake, root, map[string]string{"D/a.txt": "a", "D/T/x.txt": "x"}, "", a, b)
}

// S020.5 — D deleted here and a new, unrelated folder made here under the
// name Drive renamed D to: the disk seems to show the rename, without
// evidence (no file of D found in it), so the two are strangers (§4.3, S002)
// and D's files come back into E beside the new one, which goes up. Was:
// D's files trashed on Drive.
func TestS020FolderDeletedHereNewFolderAtTheRenamedName(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "D/x.txt", "x")
	a.write(t, "D/y.txt", "y")
	settleAll(t, a, b)
	trashed := fake.TrashCount()

	a.remove(t, "D")
	a.write(t, "E/u.txt", "u")
	b.rename(t, "D", "E")
	b.sync(t)

	s020NoDeletion(t, a, a.sync(t))
	if got := fake.TrashCount(); got != trashed {
		t.Fatalf("Drive's bin gained %d entries, want none", got-trashed)
	}
	settleAll(t, a, b)
	wantTrees(t, fake, root, map[string]string{"E/x.txt": "x", "E/y.txt": "y", "E/u.txt": "u"}, "", a, b)
	if dups := driveDuplicates(t, fake, root); len(dups) > 0 {
		t.Fatalf("Drive holds two items under one name: %v", dups)
	}
}

// S020.6 — the restore interrupted: a download's commit lost (disk half done).
// Recovery commits it (S035); the rest comes down; nothing is binned.
func TestS020RestoreInterruptedAtADownloadCommit(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "D/x.txt", "x")
	a.write(t, "D/y.txt", "y")
	settleAll(t, a, b)
	trashed := fake.TrashCount()

	a.remove(t, "D")
	b.rename(t, "D", "E")
	b.sync(t)

	// failOnce's latch is for serial-stage checkpoints; downloads run in the
	// transfer stage's worker pool, so this one is atomic.
	var fired atomic.Bool
	executor.FaultHook = func(name string) error {
		if name == executor.CPDownloadBeforeCommit && fired.CompareAndSwap(false, true) {
			return errors.New("injected: commit lost at " + name)
		}
		return nil
	}
	t.Cleanup(func() { executor.FaultHook = nil })
	syncFailing(t, a)
	noFault()
	settleAll(t, a, b)
	if got := fake.TrashCount(); got != trashed {
		t.Fatalf("Drive's bin gained %d entries, want none", got-trashed)
	}
	wantTrees(t, fake, root, map[string]string{"E/x.txt": "x", "E/y.txt": "y"}, "", a, b)
}

// S020.21 — renamed differently on two machines (E on Drive, F here): D is
// gone here, so E comes back (DEC-006) and F goes up as a new folder — both
// kept, as §4.3 keeps a file renamed differently on both sides. Was: a failed
// move_local D→E, the files moved on Drive into a new F, E binned.
func TestS020FolderRenamedDifferentlyOnTwoMachinesKeepsBoth(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "D/x.txt", "x")
	settleAll(t, a, b)
	trashed := fake.TrashCount()

	b.rename(t, "D", "E")
	b.sync(t)
	a.rename(t, "D", "F")

	s020NoDeletion(t, a, a.sync(t))
	settleAll(t, a, b)
	if got := fake.TrashCount(); got != trashed {
		t.Fatalf("Drive's bin gained %d entries, want none", got-trashed)
	}
	wantTrees(t, fake, root, map[string]string{"E/x.txt": "x", "F/x.txt": "x"}, "", a, b)
	if dups := driveDuplicates(t, fake, root); len(dups) > 0 {
		t.Fatalf("Drive holds two items under one name: %v", dups)
	}
}

// --- Round 2: the shapes the independent check reproduced (S020.26–31) ---

// s020Mv renames or moves a Drive item by id, as the web UI does.
func s020Mv(t *testing.T, fake *driveclient.Fake, id, parent, name string) {
	t.Helper()
	if _, err := fake.Move(context.Background(), id, parent, name); err != nil {
		t.Fatal(err)
	}
}

// s020FoldDuplicates lists names held twice in one Drive folder under the
// fold (case) — what a folding disk cannot hold apart.
func s020FoldDuplicates(t *testing.T, fake *driveclient.Fake, rootID string) []string {
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
			rel := prefix + c.Name
			if k := strings.ToLower(c.Name); seen[k] {
				dups = append(dups, rel)
			} else {
				seen[k] = true
			}
			if c.IsDir() {
				walk(c.ID, rel+"/")
			}
		}
	}
	walk(rootID, "")
	return dups
}

// s020BinsEmpty: nothing reached Drive's bin since trashed0, nor either
// machine's system bin.
func s020BinsEmpty(t *testing.T, fake *driveclient.Fake, trashed0 int, ms ...*machine) {
	t.Helper()
	if got := fake.TrashCount() - trashed0; got != 0 {
		t.Errorf("Drive's bin gained %d entries, want none", got)
	}
	for _, m := range ms {
		if ents, _ := os.ReadDir(m.bin.Dir); len(ents) > 0 {
			t.Errorf("[%s] system bin holds %d entries, want none", m.name, len(ents))
		}
	}
}

func s020HasSkip(res *Result, rel, why string) bool {
	for _, s := range res.Skips {
		if s.RelPath == rel && strings.Contains(s.Reason, why) {
			return true
		}
	}
	return false
}

// S020.26 — case-insensitive disk: a deletes D and makes an unrelated folder
// "e" (a file in it); Drive renames D to "E". On this disk "e" IS "E": the
// restore cannot land there without a folder rename that differs only in case,
// which a folder does not support yet (§5's directory arm) — so it waits,
// reported, and so does "e". Round 1: mkdir_local E landed in "e", Drive got
// sibling folders E and e, the machines then moved and binned each other's
// copies (x.txt in Drive's bin and both system bins, b failing every cycle).
// The variant with "e/x.txt" of other bytes: a download refused every cycle.
func TestS020CaseFoldStrangerAtTheNewNameWaits(t *testing.T) {
	for _, mine := range []string{"e/u.txt", "e/x.txt"} {
		t.Run(mine, func(t *testing.T) {
			fake, root := newWorld(t)
			a := newMachine(t, "a", fake, root)
			b := newMachine(t, "b", fake, root)
			if !names.CaseInsensitiveFS(a.dir) {
				t.Skip("sync dir is on a case-sensitive filesystem")
			}
			a.write(t, "D/x.txt", "x")
			a.write(t, "D/y.txt", "y")
			settleAll(t, a, b)
			trashed := fake.TrashCount()

			s020Mv(t, fake, driveIDOf(t, a, "D"), root, "E")
			a.remove(t, "D")
			a.write(t, mine, "mine")

			res := a.sync(t)
			if len(res.Plan) != 0 || !s020HasSkip(res, "D", "name clash") {
				t.Fatalf("[a] want nothing planned and D reported as a name clash; plan %+v skips %+v", res.Plan, res.Skips)
			}
			settleAll(t, a, b)
			s020BinsEmpty(t, fake, trashed, a, b)
			if d := s020FoldDuplicates(t, fake, root); len(d) > 0 {
				t.Fatalf("Drive holds names differing only in case: %v", d)
			}
			if got := driveTreeOf(t, fake, root); len(got) != 2 || got["E/x.txt"] != "x" || got["E/y.txt"] != "y" {
				t.Fatalf("Drive = %v, want E/x.txt, E/y.txt", got)
			}
			if got := a.listTree(t); len(got) != 1 || got[mine] != "mine" {
				t.Fatalf("[a] = %v, want only %s", got, mine)
			}
		})
	}
}

// S020.27 — a folder replaced here by a file of its name while Drive renamed
// it: a file is not the folder, so the folder is gone here → back at E with
// its files (DEC-006); the file is new → uploaded as D. Was (HEAD too): the
// folder's move_local renamed the FILE to E (never uploaded) and x.txt went to
// Drive's bin and b's.
func TestS020FolderReplacedByAFileHereRenamedOnDrive(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "D/x.txt", "x")
	settleAll(t, a, b)
	trashed := fake.TrashCount()

	s020Mv(t, fake, driveIDOf(t, a, "D"), root, "E")
	a.remove(t, "D")
	a.write(t, "D", "file-d")

	s020NoDeletion(t, a, a.sync(t))
	settleAll(t, a, b)
	s020BinsEmpty(t, fake, trashed, a, b)
	wantTrees(t, fake, root, map[string]string{"D": "file-d", "E/x.txt": "x"}, "", a, b)
}

// S020.28 — the merge (S035) with a subfolder deleted here: Drive renamed D to
// N, a still has D, made N/n.txt, deleted D/S. S was not renamed by itself:
// its deletion stands — one Drive-bin entry. Round 1: N/S came back on every
// machine. HEAD: S binned as two entries, a's move_local D/S failed once.
func TestS020MergeWithASubfolderDeletedHereBinsItOnce(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "D/a.txt", "a")
	a.write(t, "D/S/x.txt", "x")
	settleAll(t, a, b)
	trashed := fake.TrashCount()

	s020Mv(t, fake, driveIDOf(t, a, "D"), root, "N")
	a.write(t, "N/n.txt", "n")
	a.remove(t, "D/S")

	a.sync(t)
	if got := fake.TrashCount() - trashed; got != 1 {
		t.Fatalf("Drive's bin gained %d entries, want 1 (S with its file)", got)
	}
	// b's first cycle fails one quarantine at S's pre-move path (S037), then
	// converges.
	settleAllLoose(t, a, b)
	wantTrees(t, fake, root, map[string]string{"N/a.txt": "a", "N/n.txt": "n"}, "", a, b)
}

// S020.29 — Drive renames D onto the name of another live folder E: Drive
// holds two folders named E (§5: the snapshot keeps the first by id, the other
// is shadowed, its rows held). The restore never replaces E's live row: it
// waits, reported, nothing binned. Round 1: mkdir_local E dropped E's row and
// x.txt came down into E's folder — one local folder for two Drive folders.
func TestS020RenamedOntoALiveFolderOfThatNameWaits(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "D/x.txt", "x")
	a.write(t, "E/o.txt", "o")
	settleAll(t, a, b)
	trashed := fake.TrashCount()
	folderE := driveIDOf(t, a, "E")

	s020Mv(t, fake, driveIDOf(t, a, "D"), root, "E")
	a.remove(t, "D")

	res := a.sync(t)
	if len(res.Plan) != 0 || !s020HasSkip(res, "D", "another folder of that name") {
		t.Fatalf("[a] want nothing planned and D reported; plan %+v skips %+v", res.Plan, res.Skips)
	}
	if p := a.sync(t).Plan; len(p) != 0 {
		t.Fatalf("[a] second cycle planned %+v, want nothing", p)
	}
	if id := driveIDOf(t, a, "E"); id != folderE {
		t.Fatalf("[a] E's row holds %s, want E's own folder %s", id, folderE)
	}
	s020BinsEmpty(t, fake, trashed, a)
	if got := a.listTree(t); len(got) != 1 || got["E/o.txt"] != "o" {
		t.Fatalf("[a] = %v, want only E/o.txt", got)
	}
}

// S020.30 — a file E edited here while Drive binned it and renamed folder D
// to E: a file here, a folder in Drive, one name — the type-clash rule (§4.2:
// reported, left alone, nothing created beside it), not the conflict-copy rule
// (that one keeps a FILE an incoming file would replace). Round 1 and HEAD
// differ: round 1 uploaded the file beside folder E on Drive (a file and a
// folder under one name, a and b diverged for good); HEAD binned x.txt.
func TestS020FileEditedHereWhereDriveRenamedAFolderIsAClash(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "D/x.txt", "x")
	a.write(t, "E", "e")
	settleAll(t, a, b)

	if err := fake.Trash(context.Background(), driveIDOf(t, a, "E")); err != nil {
		t.Fatal(err)
	}
	trashed := fake.TrashCount()
	s020Mv(t, fake, driveIDOf(t, a, "D"), root, "E")
	a.remove(t, "D")
	a.write(t, "E", "e-edited-longer")

	// syncRaw: E's row stays while it waits, its Drive item gone — a held
	// row keeps its row without a mirror row (§5, as S013's held rows).
	res := a.syncRaw(t)
	if len(res.Plan) != 0 || !s020HasSkip(res, "E", "type clash") || !s020HasSkip(res, "D", "type clash") {
		t.Fatalf("[a] want nothing planned, E and D reported as a type clash; plan %+v skips %+v", res.Plan, res.Skips)
	}
	// b binned the file E (Drive binned it) and took the rename: its first
	// cycle fails the move onto the file's path once (S037).
	settleAllLoose(t, a, b)
	if d := driveDuplicates(t, fake, root); len(d) > 0 {
		t.Fatalf("Drive holds two items under one name: %v", d)
	}
	if got := fake.TrashCount(); got != trashed {
		t.Fatalf("Drive's bin gained %d entries, want none", got-trashed)
	}
	if got := a.read(t, "E"); got != "e-edited-longer" {
		t.Fatalf("[a] E = %q, want the edit kept", got)
	}
	if got := driveTreeOf(t, fake, root); len(got) != 1 || got["E/x.txt"] != "x" {
		t.Fatalf("Drive = %v, want E/x.txt", got)
	}
}

// S020.31 — Drive swapped folders D and E (by id); a deleted D. a gets D's
// folder back at E and E's at D in one cycle, no failure, nothing binned.
// HEAD: two moves failing `UNIQUE constraint failed: items.rel_path` and
// x.txt in Drive's bin. The other machine, which deleted nothing, fails its
// two folder moves every cycle — at HEAD too: a folder swap is not broken
// into steps (pre-existing; not this step).
func TestS020FolderSwapOnDriveWithOneDeletedHere(t *testing.T) {
	for _, gone := range []string{"D", "E"} {
		t.Run(gone, func(t *testing.T) {
			fake, root := newWorld(t)
			a := newMachine(t, "a", fake, root)
			b := newMachine(t, "b", fake, root)
			a.write(t, "D/x.txt", "x")
			a.write(t, "E/o.txt", "o")
			settleAll(t, a, b)
			trashed := fake.TrashCount()
			fd, fe := driveIDOf(t, a, "D"), driveIDOf(t, a, "E")
			s020Mv(t, fake, fd, root, "T")
			s020Mv(t, fake, fe, root, "D")
			s020Mv(t, fake, fd, root, "E")
			a.remove(t, gone)

			s020NoDeletion(t, a, a.sync(t))
			if p := a.sync(t).Plan; len(p) != 0 {
				t.Fatalf("[a] second cycle planned %+v, want nothing", p)
			}
			s020BinsEmpty(t, fake, trashed, a)
			if got := a.listTree(t); len(got) != 2 || got["E/x.txt"] != "x" || got["D/o.txt"] != "o" {
				t.Fatalf("[a] = %v, want E/x.txt and D/o.txt", got)
			}
			if driveIDOf(t, a, "E") != fd || driveIDOf(t, a, "D") != fe {
				t.Fatalf("[a] rows E, D not bound to the swapped folders")
			}
		})
	}
}
