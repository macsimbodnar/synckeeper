package engine

// S030 (DEC-011) — on a case-insensitive disk, when what Drive wants at a path
// is not the file already here under a name differing only in case: nothing is
// planned onto that path, the file here and its row stay as they are, and the
// other item is a reported skip of kind name conflict naming both files.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/names"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// s030NameConflict returns the name-conflict skip naming drive and local, if any.
func s030NameConflict(res *Result, drive, local string) (reconcile.Skip, bool) {
	for _, s := range res.Skips {
		if s.Kind == reconcile.SkipNameConflict && s.DrivePath == drive && s.LocalPath == local {
			return s, true
		}
	}
	return reconcile.Skip{}, false
}

// s030Burn spends fake ids until the next one sorts before "fake-2", so a file
// made next wins Drive's "first by id" over the files made so far.
func s030Burn(t *testing.T, fake *driveclient.Fake, root string) {
	t.Helper()
	for {
		d, err := fake.Mkdir(context.Background(), root, "burn")
		if err != nil {
			t.Fatal(err)
		}
		if err := fake.Trash(context.Background(), d.ID); err != nil {
			t.Fatal(err)
		}
		if d.ID == "fake-9" {
			return
		}
	}
}

func s030Upload(t *testing.T, fake *driveclient.Fake, parent, name, content string) string {
	t.Helper()
	f, err := fake.Upload(context.Background(), parent, name, strings.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	return f.ID
}

// S030.1 — the step's main shape. a holds Readme.txt (synced); Drive gains
// README.txt (a case-sensitive machine, the web UI) with an id that wins the
// fold. a plans nothing, fails nothing, reports a name conflict naming both,
// keeps Readme.txt — every cycle. Was: download README.txt refused every cycle
// ("a file appeared after the scan").
func TestS030FoldWinnerIsNotTheFileHere(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	if !names.CaseInsensitiveFS(a.dir) {
		t.Skip("sync dir is on a case-sensitive filesystem")
	}
	a.write(t, "Readme.txt", "mine")
	a.sync(t)
	s030Burn(t, fake, root)
	theirs := s030Upload(t, fake, root, "README.txt", "theirs")
	trashed := fake.TrashCount()

	for i := 0; i < 2; i++ {
		res := a.sync(t)
		if len(res.Plan) != 0 {
			t.Fatalf("cycle %d: want nothing planned, got %+v", i, res.Plan)
		}
		s, ok := s030NameConflict(res, "README.txt", "Readme.txt")
		if !ok {
			t.Fatalf("cycle %d: no name-conflict skip naming README.txt and Readme.txt: %+v", i, res.Skips)
		}
		if !strings.Contains(s.Reason, "README.txt") || !strings.Contains(s.Reason, "Readme.txt") {
			t.Errorf("cycle %d: the reason must name both files: %q", i, s.Reason)
		}
	}
	if got := a.listTree(t); len(got) != 1 || got["Readme.txt"] != "mine" {
		t.Fatalf("[a] = %v, want only Readme.txt = mine", got)
	}
	if fake.TrashCount() != trashed {
		t.Fatal("something went to Drive's bin")
	}

	// Renamed in Drive: the hold releases by itself.
	if _, err := fake.Move(context.Background(), theirs, root, "Theirs.txt"); err != nil {
		t.Fatal(err)
	}
	settleAll(t, a)
	if got := a.listTree(t); len(got) != 2 || got["Readme.txt"] != "mine" || got["Theirs.txt"] != "theirs" {
		t.Fatalf("[a] after the rename = %v, want Readme.txt, Theirs.txt", got)
	}
	if res := a.sync(t); len(res.Skips) != 0 {
		t.Fatalf("still reported after the rename: %+v", res.Skips)
	}
}

// S030.2 — the same with the newcomer first by id while a has not synced the
// name yet... and the inverse order: the file here wins the fold (it is
// Drive's pick), so nothing is in conflict: the newcomer is the ordinary
// case-collision skip and Readme.txt keeps syncing.
func TestS030FoldWinnerIsTheFileHere(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	if !names.CaseInsensitiveFS(a.dir) {
		t.Skip("sync dir is on a case-sensitive filesystem")
	}
	a.write(t, "Readme.txt", "mine")
	a.sync(t)
	s030Upload(t, fake, root, "README.txt", "theirs") // fake-3: loses the fold

	res := a.sync(t)
	if len(res.Plan) != 0 {
		t.Fatalf("want nothing planned, got %+v", res.Plan)
	}
	for _, s := range res.Skips {
		if s.Kind == reconcile.SkipNameConflict {
			t.Fatalf("Drive's pick is the file here: no name conflict, got %+v", s)
		}
	}
	a.write(t, "Readme.txt", "mine, edited")
	a.sync(t)
	if got := driveTreeOf(t, fake, root); got["Readme.txt"] != "mine, edited" || got["README.txt"] != "theirs" {
		t.Fatalf("Drive = %v, want the edit up and README.txt untouched", got)
	}
}

// S030.3 — shape (b), the minimized repro of fuzz 200×150 seed 102. b and c
// synced; c renames db/f0.txt → da/f0.txt; b writes a new da/F0.txt. On b one
// path, two files: the move is not planned, da/F0.txt not uploaded (Drive would
// hold two names differing only in case), a name conflict reported, 0 failed;
// every file kept. Renaming da/F0.txt releases it: all converge. Was: b
// uploaded da/F0.txt and failed move_local db/f0.txt → da/f0.txt every cycle.
func TestS030MoveOntoANewFileDifferingInCase(t *testing.T) {
	fake, root := newWorld(t)
	b := newMachine(t, "b", fake, root)
	c := newMachine(t, "c", fake, root)
	if !names.CaseInsensitiveFS(b.dir) {
		t.Skip("sync dir is on a case-sensitive filesystem")
	}
	b.write(t, "db/f0.txt", "f0")
	b.write(t, "da/x.txt", "x")
	settleAll(t, b, c)
	trashed := fake.TrashCount()

	c.rename(t, "db/f0.txt", "da/f0.txt")
	c.sync(t)
	b.write(t, "da/F0.txt", "new")

	for i := 0; i < 2; i++ {
		res := b.sync(t)
		for _, act := range res.Plan {
			if act.RelPath == "da/F0.txt" || act.NewRelPath == "da/f0.txt" || act.RelPath == "da/f0.txt" {
				t.Fatalf("cycle %d: planned %+v onto the name both files want", i, act)
			}
		}
		if _, ok := s030NameConflict(res, "da/f0.txt", "da/F0.txt"); !ok {
			t.Fatalf("cycle %d: no name-conflict skip naming da/f0.txt and da/F0.txt: %+v", i, res.Skips)
		}
	}
	if d := s020FoldDuplicates(t, fake, root); len(d) > 0 {
		t.Fatalf("Drive holds names differing only in case: %v", d)
	}
	if got := b.listTree(t); len(got) != 3 || got["db/f0.txt"] != "f0" || got["da/F0.txt"] != "new" || got["da/x.txt"] != "x" {
		t.Fatalf("[b] = %v, want db/f0.txt, da/F0.txt, da/x.txt untouched", got)
	}

	// The user renames the new file: the hold releases, everyone converges.
	b.rename(t, "da/F0.txt", "da/g.txt")
	settleAll(t, b, c)
	wantTrees(t, fake, root, map[string]string{"da/f0.txt": "f0", "da/g.txt": "new", "da/x.txt": "x"}, "", b, c)
	s020BinsEmpty(t, fake, trashed, b, c)
}

// S030.4 — shape (b) the other way round, a guard: b's new da/F0.txt reached
// Drive first; then c, which renamed db/f0.txt → da/f0.txt here, syncs. That
// is §5's fold-equal both-new pair (C2/R19), which runs before move pairing: a
// conflict copy of c's file, Drive's da/F0.txt down, db/f0.txt binned on
// Drive and on b. Converges, nothing lost, no Drive name held twice, no
// failed action; the rename itself is not kept (pre-existing, unchanged).
func TestS030RenameOntoANameDriveHoldsInAnotherCase(t *testing.T) {
	fake, root := newWorld(t)
	b := newMachine(t, "b", fake, root)
	c := newMachine(t, "c", fake, root)
	if !names.CaseInsensitiveFS(b.dir) {
		t.Skip("sync dir is on a case-sensitive filesystem")
	}
	b.write(t, "db/f0.txt", "f0")
	b.write(t, "da/x.txt", "x")
	settleAll(t, b, c)

	b.write(t, "da/F0.txt", "new")
	b.sync(t)
	c.rename(t, "db/f0.txt", "da/f0.txt")
	settleAll(t, b, c)
	if d := s020FoldDuplicates(t, fake, root); len(d) > 0 {
		t.Fatalf("Drive holds names differing only in case: %v", d)
	}
	drive := driveTreeOf(t, fake, root)
	for where, tree := range map[string]map[string]string{"drive": drive, "b": b.listTree(t), "c": c.listTree(t)} {
		seen := map[string]bool{}
		for _, v := range tree {
			seen[v] = true
		}
		if !seen["f0"] || !seen["new"] || len(tree) != len(drive) {
			t.Errorf("[%s] = %v, want both contents and Drive's tree", where, tree)
		}
	}
}

// S030.11 — the typed skip reaches every place skips are kept: the result
// (`sync`, `init` print it) and the stored last-skipped list (meta
// last_skipped, JSON), with its kind and both names. A list stored before
// S030 (no such fields) still reads, as untyped skips.
func TestS030NameConflictIsStoredTyped(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	if !names.CaseInsensitiveFS(a.dir) {
		t.Skip("sync dir is on a case-sensitive filesystem")
	}
	a.write(t, "Readme.txt", "mine")
	a.sync(t)
	s030Burn(t, fake, root)
	s030Upload(t, fake, root, "README.txt", "theirs")
	a.sync(t)

	raw, err := a.db.GetMeta(MetaLastSkipped)
	if err != nil {
		t.Fatal(err)
	}
	var stored []reconcile.Skip
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range stored {
		if s.Kind == reconcile.SkipNameConflict && s.RelPath == "README.txt" && s.DrivePath == "README.txt" && s.LocalPath == "Readme.txt" {
			found = true
		}
	}
	if !found || !strings.Contains(raw, `"Kind":"name_conflict"`) {
		t.Fatalf("stored skips lack the typed name conflict: %s", raw)
	}

	var old []reconcile.Skip
	if err := json.Unmarshal([]byte(`[{"RelPath":"x","Reason":"r","FileID":"","Unreadable":false,"Unscanned":false,"UnreadableFile":false,"Size":0,"MtimeNS":0,"Duplicate":false}]`), &old); err != nil {
		t.Fatal(err)
	}
	if len(old) != 1 || old[0].Kind != "" || old[0].RelPath != "x" {
		t.Fatalf("a pre-S030 list reads as %+v", old)
	}
}

// --- Round 2 (S030.17–) ---

// s030Main: a and b hold F/Readme.txt (B) and F/keep.txt; Drive gains
// F/README.txt (A), which wins the fold; a reports the name conflict.
func s030Main(t *testing.T) (fake *driveclient.Fake, root string, a, b *machine, theirs, mine string) {
	t.Helper()
	fake, root = newWorld(t)
	a = newMachine(t, "a", fake, root)
	b = newMachine(t, "b", fake, root)
	if !names.CaseInsensitiveFS(a.dir) {
		t.Skip("sync dir is on a case-sensitive filesystem")
	}
	a.write(t, "F/Readme.txt", "mine")
	a.write(t, "F/keep.txt", "keep")
	settleAll(t, a, b)
	mine = driveIDOf(t, a, "F/Readme.txt")
	s030Burn(t, fake, root)
	theirs = s030Upload(t, fake, driveIDOf(t, a, "F"), "README.txt", "theirs")
	if _, ok := s030NameConflict(a.sync(t), "F/README.txt", "F/Readme.txt"); !ok {
		t.Fatal("setup: no name conflict on a")
	}
	return
}

// S030.17 — the held file renamed (or moved) here releases the conflict as a
// Drive rename of its own item: B goes to the new name, A comes down as
// README.txt, b follows; one Drive item per file, nothing binned, no failed
// action, nothing reported. Was (round 1): Mine.txt uploaded as a NEW file,
// Drive keeping B too (content twice), b still reporting the conflict.
func TestS030HeldFileRenamedHereReleases(t *testing.T) {
	for _, to := range []string{"F/Mine.txt", "Other/Mine.txt"} {
		t.Run(to, func(t *testing.T) {
			fake, root, a, b, _, mine := s030Main(t)
			trashed := fake.TrashCount()
			a.rename(t, "F/Readme.txt", to)
			settleAll(t, a, b)
			wantTrees(t, fake, root, map[string]string{to: "mine", "F/README.txt": "theirs", "F/keep.txt": "keep"}, "", a, b)
			if got := driveIDOf(t, a, to); got != mine {
				t.Errorf("%s bound to %s, want B's id %s (a rename, not a new file)", to, got, mine)
			}
			s020BinsEmpty(t, fake, trashed, a, b)
			for _, m := range []*machine{a, b} {
				if res := m.sync(t); len(res.Skips) != 0 {
					t.Errorf("[%s] still reports %+v", m.name, res.Skips)
				}
			}
		})
	}
}

// S030.18 — the held file deleted here: the hold stands (§5, R19: a shadowed
// row is never read as deleted), so nothing is binned on Drive; A comes down
// on a; b keeps its conflict. Renamed AND edited here: no rename evidence, the
// edit is a new file (uploaded), the row stays held. Nothing lost, no failed
// action either way.
func TestS030HeldFileDeletedOrRenamedEditedHere(t *testing.T) {
	t.Run("deleted", func(t *testing.T) {
		fake, root, a, b, _, _ := s030Main(t)
		trashed := fake.TrashCount()
		a.remove(t, "F/Readme.txt")
		settleAll(t, a, b)
		s020BinsEmpty(t, fake, trashed, a, b)
		if got := driveTreeOf(t, fake, root); got["F/Readme.txt"] != "mine" || got["F/README.txt"] != "theirs" {
			t.Fatalf("Drive = %v, want both kept", got)
		}
		if got := a.listTree(t); got["F/README.txt"] != "theirs" || len(got) != 2 {
			t.Fatalf("[a] = %v, want F/README.txt, F/keep.txt", got)
		}
	})
	t.Run("renamed and edited", func(t *testing.T) {
		fake, root, a, b, _, _ := s030Main(t)
		trashed := fake.TrashCount()
		a.rename(t, "F/Readme.txt", "F/Mine.txt")
		a.write(t, "F/Mine.txt", "mine, edited here")
		settleAll(t, a, b)
		s020BinsEmpty(t, fake, trashed, a, b)
		got := driveTreeOf(t, fake, root)
		if got["F/Readme.txt"] != "mine" || got["F/README.txt"] != "theirs" || got["F/Mine.txt"] != "mine, edited here" {
			t.Fatalf("Drive = %v, want all three kept", got)
		}
	})
}

// S030.19 — the conflict inside a folder renamed this cycle, on Drive or
// here: no failed action (m.sync fails on one); the conflict reported under
// the new name. Was: download G/README.txt refused once by the gate.
func TestS030ConflictInAFolderRenamedThisCycle(t *testing.T) {
	for _, where := range []string{"drive", "here"} {
		t.Run(where, func(t *testing.T) {
			fake, root, a, b, _, _ := s030Main(t)
			if where == "drive" {
				s020Mv(t, fake, driveIDOf(t, a, "F"), root, "G")
			} else {
				a.rename(t, "F", "G")
			}
			for i := 0; i < 2; i++ {
				for _, m := range []*machine{a, b} {
					m.sync(t)
				}
			}
			for _, m := range []*machine{a, b} {
				if _, ok := s030NameConflict(m.sync(t), "G/README.txt", "G/Readme.txt"); !ok {
					t.Errorf("[%s] no name conflict G/README.txt / G/Readme.txt", m.name)
				}
			}
		})
	}
}

// s030Pair: a and b hold a.txt.
func s030Pair(t *testing.T) (fake *driveclient.Fake, root string, a, b *machine) {
	t.Helper()
	fake, root = newWorld(t)
	a = newMachine(t, "a", fake, root)
	b = newMachine(t, "b", fake, root)
	if !names.CaseInsensitiveFS(a.dir) {
		t.Skip("sync dir is on a case-sensitive filesystem")
	}
	a.write(t, "a.txt", "a")
	a.write(t, "keep.txt", "keep")
	settleAll(t, a, b)
	return
}

// S030.20 — a case-only rename is the same file, never a name conflict:
// (1) renamed on Drive (or on another machine) → followed here, no failed
// action (was, at HEAD too: move_local refused every cycle — MANUAL §8);
// (2) renamed in case differently here and on Drive → Drive's name wins (the
// remote keeps the canonical name, §4.2/§5: a folding disk holds one); (3)
// renamed here while Drive edits it → the rename goes up, the edit comes down.
func TestS030CaseOnlyRenameIsTheSameFile(t *testing.T) {
	t.Run("on another machine", func(t *testing.T) {
		fake, root, a, b := s030Pair(t)
		a.rename(t, "a.txt", "A.txt")
		settleAll(t, a, b)
		wantTrees(t, fake, root, map[string]string{"A.txt": "a", "keep.txt": "keep"}, "", a, b)
	})
	t.Run("on Drive and here, differently", func(t *testing.T) {
		fake, root, a, b := s030Pair(t)
		s020Mv(t, fake, driveIDOf(t, a, "a.txt"), root, "A.txt")
		a.rename(t, "a.txt", "A.TXT")
		settleAll(t, a, b)
		wantTrees(t, fake, root, map[string]string{"A.txt": "a", "keep.txt": "keep"}, "", a, b)
	})
	t.Run("here, edited on Drive", func(t *testing.T) {
		fake, root, a, b := s030Pair(t)
		id := driveIDOf(t, a, "a.txt")
		a.rename(t, "a.txt", "A.txt")
		if _, err := fake.Update(context.Background(), id, strings.NewReader("a edited in drive"), 17); err != nil {
			t.Fatal(err)
		}
		settleAll(t, a, b)
		wantTrees(t, fake, root, map[string]string{"A.txt": "a edited in drive", "keep.txt": "keep"}, "", a, b)
		if got := driveIDOf(t, a, "A.txt"); got != id {
			t.Errorf("A.txt bound to %s, want %s", got, id)
		}
	})
	t.Run("here, edited here", func(t *testing.T) {
		fake, root, a, b := s030Pair(t)
		id := driveIDOf(t, a, "a.txt")
		trashed := fake.TrashCount()
		a.rename(t, "a.txt", "A.txt")
		a.write(t, "A.txt", "A edited here, longer")
		settleAll(t, a, b)
		wantTrees(t, fake, root, map[string]string{"A.txt": "A edited here, longer", "keep.txt": "keep"}, "", a, b)
		if got := driveIDOf(t, a, "A.txt"); got != id {
			t.Errorf("A.txt bound to %s, want %s (renamed and updated, not re-uploaded)", got, id)
		}
		s020BinsEmpty(t, fake, trashed, a, b)
	})
}
