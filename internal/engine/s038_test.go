package engine

// S038 — on a case-insensitive disk (macOS default), a FOLDER whose name here
// differs only in case (or accents) from the name Drive uses. One rule, the
// one S030 built for files: a folder here at a folder's own path under the
// fold is that folder; a case-only rename of a folder is the folder's own
// rename, synced as a rename on every machine; renamed in case differently on
// two machines → Drive's name wins; no plan puts on Drive a name differing only
// by the fold from one Drive keeps, and none creates here a folder at a path a
// different local entry holds — that is a reported, typed name conflict.

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/executor"
	"github.com/macsimbodnar/synckeeper/internal/names"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// s038Pair: a and b, both on this (folding) disk, synced D/x.txt and D/y.txt.
func s038Pair(t *testing.T) (fake *driveclient.Fake, root string, a, b *machine, trashed int) {
	t.Helper()
	fake, root = newWorld(t)
	a = newMachine(t, "a", fake, root)
	b = newMachine(t, "b", fake, root)
	if !names.CaseInsensitiveFS(a.dir) {
		t.Skip("sync dir is on a case-sensitive filesystem")
	}
	a.write(t, "D/x.txt", "x")
	a.write(t, "D/y.txt", "y")
	settleAll(t, a, b)
	return fake, root, a, b, fake.TrashCount()
}

// s038NoMint: no plan action creates on Drive a folder, or puts a file, under
// a name that only the fold tells from another: Drive holds no such pair.
func s038NoMint(t *testing.T, fake *driveclient.Fake, root string) {
	t.Helper()
	if d := s020FoldDuplicates(t, fake, root); len(d) > 0 {
		t.Fatalf("Drive holds names differing only in case: %v", d)
	}
}

// s038Dirs lists the folders under a machine's sync dir, by their names as
// stored on disk.
func s038Dirs(t *testing.T, m *machine) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	var walk func(abs, rel string)
	walk = func(abs, rel string) {
		ents, err := os.ReadDir(abs)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ents {
			if !e.IsDir() {
				continue
			}
			r := e.Name()
			if rel != "" {
				r = rel + "/" + r
			}
			out[r] = true
			walk(abs+"/"+e.Name(), r)
		}
	}
	walk(m.dir, "")
	return out
}

// s038Settled: the machines settle with no failed action (settleAll fails on
// one), Drive mints no fold pair, every tree — Drive's, each machine's, with
// the folder names as stored — is want, nothing reached a bin, and the Drive
// folder now at dir is still the one with id dirID.
func s038Settled(t *testing.T, fake *driveclient.Fake, root string, trashed int, want map[string]string, dir, dirID string, ms ...*machine) {
	t.Helper()
	settleAll(t, ms...)
	s038NoMint(t, fake, root)
	wantTrees(t, fake, root, want, "", ms...)
	s020BinsEmpty(t, fake, trashed, ms...)
	for _, m := range ms {
		if dirs := s038Dirs(t, m); !dirs[dir] {
			t.Errorf("[%s] folders %v, want %s spelled so", m.name, dirs, dir)
		}
		if got := driveIDOf(t, m, dir); got != dirID {
			t.Errorf("[%s] %s bound to %s, want %s (the folder renamed, not re-created)", m.name, dir, got, dirID)
		}
	}
}

// S038.1 — the step's reproduced shape. a and b synced D/x.txt, D/y.txt; a
// renames D → e here, b renames D → E (Drive). Drive's name wins: a renames e
// to E, the folder keeps its Drive id, its files theirs; no mkdir_remote e, no
// trash. Variants: b also edits E/x.txt, adds E/z.txt, adds E/S/s.txt — before
// a's first cycle or after it. Was (HEAD): a planned move_local D→E,
// mkdir_remote e, move_remote D/x.txt→e/x.txt, D/y.txt→e/y.txt, trash_remote E —
// Drive held E and e; with b's edit a failed every cycle and y.txt went to b's
// bin; b's new z.txt ended only in b's bin; E/S/s.txt never settled.
func TestS038FolderRenamedInCaseDifferentlyHereAndOnDrive(t *testing.T) {
	type variant struct {
		name string
		op   func(t *testing.T, b *machine)
		want map[string]string
	}
	variants := []variant{
		{"nothing else", func(*testing.T, *machine) {}, map[string]string{"E/x.txt": "x", "E/y.txt": "y"}},
		{"b edits E/x.txt", func(t *testing.T, b *machine) { b.write(t, "E/x.txt", "x, edited by b") },
			map[string]string{"E/x.txt": "x, edited by b", "E/y.txt": "y"}},
		{"b adds E/z.txt", func(t *testing.T, b *machine) { b.write(t, "E/z.txt", "z") },
			map[string]string{"E/x.txt": "x", "E/y.txt": "y", "E/z.txt": "z"}},
		{"b adds E/S/s.txt", func(t *testing.T, b *machine) { b.write(t, "E/S/s.txt", "s") },
			map[string]string{"E/x.txt": "x", "E/y.txt": "y", "E/S/s.txt": "s"}},
	}
	for _, v := range variants {
		for _, when := range []string{"before", "after"} {
			t.Run(v.name+", "+when+" a's first cycle", func(t *testing.T) {
				fake, root, a, b, trashed := s038Pair(t)
				dID := driveIDOf(t, a, "D")
				b.rename(t, "D", "E")
				b.sync(t)
				if when == "before" {
					v.op(t, b)
					b.sync(t)
				}
				a.rename(t, "D", "e")
				res := a.sync(t)
				for _, act := range res.Plan {
					if act.Type == reconcile.MkdirRemote || act.Type == reconcile.TrashRemote ||
						act.Type == reconcile.Upload || act.Type == reconcile.QuarantineLocal {
						t.Fatalf("[a] first cycle planned %s %s: %+v", act.Type, act.RelPath, res.Plan)
					}
				}
				s038NoMint(t, fake, root)
				if when == "after" {
					v.op(t, b)
					b.sync(t)
				}
				s038Settled(t, fake, root, trashed, v.want, "E", dID, a, b)
			})
		}
	}
}

// S038.2 — a case-only folder rename here (D → d) is the folder's own rename:
// one move_remote of the folder, its id and its files' ids kept; b follows
// with one move_local, its disk spelling d too. Also with an edit and a new
// file inside the renamed folder. Was: the rows read as deleted here (row D/x,
// disk d/x), d made on Drive beside D, the files moved into it, D binned.
func TestS038CaseOnlyFolderRenameHere(t *testing.T) {
	t.Run("plain", func(t *testing.T) {
		fake, root, a, b, trashed := s038Pair(t)
		dID := driveIDOf(t, a, "D")
		a.rename(t, "D", "d")
		res := a.sync(t)
		if len(res.Plan) != 1 || res.Plan[0].Type != reconcile.MoveRemote || !res.Plan[0].IsDir ||
			res.Plan[0].RelPath != "D" || res.Plan[0].NewRelPath != "d" {
			t.Fatalf("[a] want one move_remote D → d, got %+v", res.Plan)
		}
		s038Settled(t, fake, root, trashed, map[string]string{"d/x.txt": "x", "d/y.txt": "y"}, "d", dID, a, b)
	})
	t.Run("edited and added inside", func(t *testing.T) {
		fake, root, a, b, trashed := s038Pair(t)
		dID, xID := driveIDOf(t, a, "D"), driveIDOf(t, a, "D/x.txt")
		a.rename(t, "D", "d")
		a.write(t, "d/x.txt", "x, edited here")
		a.write(t, "d/w.txt", "w")
		s038Settled(t, fake, root, trashed, map[string]string{"d/x.txt": "x, edited here", "d/y.txt": "y", "d/w.txt": "w"},
			"d", dID, a, b)
		if got := driveIDOf(t, b, "d/x.txt"); got != xID {
			t.Errorf("d/x.txt bound to %s, want %s (updated, not re-uploaded)", got, xID)
		}
	})
	t.Run("nested folder, inner renamed in case", func(t *testing.T) {
		fake, root, a, b, trashed := s038Pair(t)
		a.write(t, "D/S/s.txt", "s")
		settleAll(t, a, b)
		sID := driveIDOf(t, a, "D/S")
		a.rename(t, "D/S", "D/s")
		s038Settled(t, fake, root, trashed, map[string]string{"D/x.txt": "x", "D/y.txt": "y", "D/s/s.txt": "s"},
			"D/s", sID, a, b)
	})
	t.Run("outer and inner renamed in case", func(t *testing.T) {
		fake, root, a, b, trashed := s038Pair(t)
		a.write(t, "D/S/s.txt", "s")
		settleAll(t, a, b)
		sID := driveIDOf(t, a, "D/S")
		a.rename(t, "D", "d")
		a.rename(t, "d/S", "d/s")
		s038Settled(t, fake, root, trashed, map[string]string{"d/x.txt": "x", "d/y.txt": "y", "d/s/s.txt": "s"},
			"d/s", sID, a, b)
	})
}

// S038.3 — a case-only folder rename on Drive (D → d, the web UI or a
// case-sensitive machine) is followed here with one move_local whose
// destination folds to its source; the disk then spells it d. Was: move_local
// D → d refused every cycle ("a file appeared after the scan").
func TestS038CaseOnlyFolderRenameOnDrive(t *testing.T) {
	fake, root, a, b, trashed := s038Pair(t)
	dID := driveIDOf(t, a, "D")
	s020Mv(t, fake, dID, root, "d")
	res := a.sync(t)
	if len(res.Plan) != 1 || res.Plan[0].Type != reconcile.MoveLocal || !res.Plan[0].IsDir {
		t.Fatalf("[a] want one move_local D → d, got %+v", res.Plan)
	}
	if res := a.sync(t); len(res.Plan) != 0 {
		t.Fatalf("[a] second cycle not idle: %+v", res.Plan)
	}
	s038Settled(t, fake, root, trashed, map[string]string{"d/x.txt": "x", "d/y.txt": "y"}, "d", dID, a, b)
}

// S038.4 — renamed in case differently here and on Drive: Drive's name wins,
// as for a file (S030): the disk ends spelled as Drive. Both a case-only
// rename (Doc → doc here, → DOC on Drive) and two spellings of a new name
// (D → dd here, → DD on Drive).
func TestS038CaseOnlyFolderRenameHereAndOnDriveDifferently(t *testing.T) {
	t.Run("case only", func(t *testing.T) {
		fake, root, a, b, trashed := s038Pair(t)
		a.rename(t, "D", "Doc")
		settleAll(t, a, b)
		dID := driveIDOf(t, a, "Doc")
		s020Mv(t, fake, dID, root, "DOC")
		a.rename(t, "Doc", "doc")
		s038Settled(t, fake, root, trashed, map[string]string{"DOC/x.txt": "x", "DOC/y.txt": "y"}, "DOC", dID, a, b)
	})
	t.Run("new name, two spellings", func(t *testing.T) {
		fake, root, a, b, trashed := s038Pair(t)
		dID := driveIDOf(t, a, "D")
		s020Mv(t, fake, dID, root, "DD")
		a.rename(t, "D", "dd")
		s038Settled(t, fake, root, trashed, map[string]string{"DD/x.txt": "x", "DD/y.txt": "y"}, "DD", dID, a, b)
	})
}

// S038.5 — crash safety (§4.6): the case-only folder rename's commit is lost
// after the disk half ran (move_before_commit). Recovery commits it; the next
// cycles are idle on a and converge with b; nothing binned or duplicated.
func TestS038CaseOnlyFolderRenameCommitLost(t *testing.T) {
	fake, root, a, b, trashed := s038Pair(t)
	dID := driveIDOf(t, a, "D")
	s020Mv(t, fake, dID, root, "d")
	failOnce(t, executor.CPMoveBeforeCommit)
	syncFailing(t, a)
	noFault()
	if res := a.sync(t); len(res.Plan) != 0 {
		t.Fatalf("[a] after recovery want nothing planned, got %+v", res.Plan)
	}
	s038Settled(t, fake, root, trashed, map[string]string{"d/x.txt": "x", "d/y.txt": "y"}, "d", dID, a, b)
}

// s038HasConflict: a name-conflict skip naming drive (Drive's name) and local.
func s038HasConflict(res *Result, drive, local string) bool {
	_, ok := s030NameConflict(res, drive, local)
	return ok
}

// s038HeldAs: rel is reported as a name conflict naming drive and local.
func s038HeldAs(res *Result, rel, drive, local string) bool {
	for _, s := range res.Skips {
		if s.RelPath == rel && s.Kind == reconcile.SkipNameConflict && s.DrivePath == drive && s.LocalPath == local {
			return true
		}
	}
	return false
}

// S038.6 — two genuinely different folders whose names fold together: Drive
// gains a folder d (another machine, the web UI) beside a's synced D, with a
// file in it, and d wins Drive's fold (first by id). a's D is held (R24); d is
// a name conflict naming both, and so is d/z.txt: nothing lands in a's D.
// Releases by renaming either. Was: d/z.txt downloaded into D (the disk's d),
// its row then read as deleted here and binned on Drive.
func TestS038TwoFoldEqualFoldersOnDrive(t *testing.T) {
	fake, root, a, _, _ := s038Pair(t)
	s030Burn(t, fake, root)
	trashed := fake.TrashCount()
	d, err := fake.Mkdir(context.Background(), root, "d")
	if err != nil {
		t.Fatal(err)
	}
	s030Upload(t, fake, d.ID, "z.txt", "z")
	for i := 0; i < 2; i++ {
		res := a.sync(t)
		if len(res.Plan) != 0 {
			t.Fatalf("cycle %d: want nothing planned, got %+v", i, res.Plan)
		}
		if !s038HeldAs(res, "d", "d", "D") || !s038HeldAs(res, "d/z.txt", "d", "D") {
			t.Fatalf("cycle %d: want name conflicts for d and d/z.txt naming D: %+v", i, res.Skips)
		}
	}
	if got := a.listTree(t); len(got) != 2 || got["D/x.txt"] != "x" || got["D/y.txt"] != "y" {
		t.Fatalf("[a] = %v, want D/x.txt, D/y.txt only", got)
	}
	if fake.TrashCount() != trashed {
		t.Fatal("something went to Drive's bin")
	}
	s020Mv(t, fake, d.ID, root, "d2")
	settleAll(t, a)
	if got := a.listTree(t); len(got) != 3 || got["d2/z.txt"] != "z" {
		t.Fatalf("[a] after the rename = %v, want d2/z.txt too", got)
	}
}

// S038.7 — a new folder here beside a new Drive FILE whose name differs only in
// case, and the reverse (a new Drive folder beside a new file here): one name
// on this disk → a name conflict naming both, no mkdir_remote of the other
// spelling, no download into the folder's place, zero failed actions. Was: a
// folder f.txt minted beside Drive's F.txt; F.txt/inner.txt failed every cycle
// (its parent is a file here).
func TestS038FolderVersusFileDifferingInCase(t *testing.T) {
	t.Run("folder here, file on Drive", func(t *testing.T) {
		fake, root, a, b, _ := s038Pair(t)
		b.write(t, "F.txt", "file")
		b.sync(t)
		a.write(t, "f.txt/inner.txt", "inner")
		for i := 0; i < 2; i++ {
			res := a.sync(t)
			if len(res.Plan) != 0 {
				t.Fatalf("cycle %d: want nothing planned, got %+v", i, res.Plan)
			}
			if !s038HeldAs(res, "f.txt", "F.txt", "f.txt") || !s038HeldAs(res, "f.txt/inner.txt", "F.txt", "f.txt") {
				t.Fatalf("cycle %d: want f.txt and f.txt/inner.txt held, naming F.txt and f.txt: %+v", i, res.Skips)
			}
		}
		s038NoMint(t, fake, root)
		if got := a.listTree(t); got["f.txt/inner.txt"] != "inner" {
			t.Fatalf("[a] = %v, want f.txt/inner.txt kept", got)
		}
	})
	t.Run("file here, folder on Drive", func(t *testing.T) {
		fake, root, a, b, _ := s038Pair(t)
		b.write(t, "F.txt/inner.txt", "inner")
		b.sync(t)
		a.write(t, "f.txt", "file")
		for i := 0; i < 2; i++ {
			res := a.sync(t)
			if len(res.Plan) != 0 {
				t.Fatalf("cycle %d: want nothing planned, got %+v", i, res.Plan)
			}
			if !s038HeldAs(res, "F.txt", "F.txt", "f.txt") || !s038HeldAs(res, "F.txt/inner.txt", "F.txt", "f.txt") {
				t.Fatalf("cycle %d: want name conflicts for F.txt and F.txt/inner.txt naming f.txt: %+v", i, res.Skips)
			}
		}
		s038NoMint(t, fake, root)
		if got := a.listTree(t); got["f.txt"] != "file" {
			t.Fatalf("[a] = %v, want f.txt kept", got)
		}
	})
}

// S038.8 — a new folder here and a new Drive folder whose names differ only in
// case are one folder on this disk (the folder flavour of §5's fold adopt,
// C2): adopted at Drive's spelling, the disk renamed to it, content merged by
// §4.2's both-new rows; no second Drive folder. Was: mkdir_remote e beside E.
func TestS038NewFoldersDifferingInCaseAreAdopted(t *testing.T) {
	fake, root, a, b, trashed := s038Pair(t)
	b.write(t, "E/b.txt", "from b")
	b.sync(t)
	eID := driveIDOf(t, b, "E")
	a.write(t, "e/a.txt", "from a")
	s038Settled(t, fake, root, trashed,
		map[string]string{"D/x.txt": "x", "D/y.txt": "y", "E/a.txt": "from a", "E/b.txt": "from b"}, "E", eID, a, b)
}

// S038.9 — a folder Drive renamed onto a name a different folder here holds in
// another case: the rename cannot land (one name on this disk) — a name
// conflict naming both, the folder and its files left where they are, nothing
// binned, zero failed actions; renaming the folder here releases it. Was:
// move_local D → E refused every cycle ("a file appeared after the scan").
func TestS038DriveRenameOntoAFoldEqualFolderHere(t *testing.T) {
	fake, root, a, b, trashed := s038Pair(t)
	s020Mv(t, fake, driveIDOf(t, a, "D"), root, "E")
	a.write(t, "e/mine.txt", "mine")
	for i := 0; i < 2; i++ {
		res := a.sync(t)
		for _, act := range res.Plan {
			if act.Type == reconcile.MoveLocal || act.Type == reconcile.MkdirRemote || act.RelPath == "e" {
				t.Fatalf("cycle %d: planned %+v", i, act)
			}
		}
		if !s038HasConflict(res, "E", "e") {
			t.Fatalf("cycle %d: no name conflict naming E and e: %+v", i, res.Skips)
		}
	}
	s038NoMint(t, fake, root)
	if got := a.listTree(t); len(got) != 3 || got["D/x.txt"] != "x" || got["e/mine.txt"] != "mine" {
		t.Fatalf("[a] = %v, want D/x.txt, D/y.txt, e/mine.txt untouched", got)
	}
	a.rename(t, "e", "mine")
	settleAll(t, a, b)
	wantTrees(t, fake, root, map[string]string{"E/x.txt": "x", "E/y.txt": "y", "mine/mine.txt": "mine"}, "", a, b)
	s020BinsEmpty(t, fake, trashed, a, b)
}

// S038.25 — rows under a folder the scan cannot read are matched to it under
// the fold (W18-G's hold): the folder renamed in case differently here (doc)
// and on Drive (DOC), doc/S unreadable. S's rows are held (not binned on
// Drive), the rest follows Drive's name; readable again, all converge. Was:
// the rows' prefix Doc/S/ did not match the scan's doc/S, the rows read as
// gone here and their Drive copies binned.
func TestS038UnreadableFolderInsideAFolderRenamedInCase(t *testing.T) {
	fake, root, a, b, trashed := s038Pair(t)
	a.write(t, "D/S/s.txt", "s")
	a.rename(t, "D", "Doc")
	settleAll(t, a, b)
	dID := driveIDOf(t, a, "Doc")
	s020Mv(t, fake, dID, root, "DOC")
	a.rename(t, "Doc", "doc")
	locked := filepath.Join(a.dir, "doc", "S")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Skipf("cannot chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(a.dir, "DOC", "S"), 0o755) })
	if _, err := os.ReadDir(locked); err == nil {
		t.Skip("this environment can read a 0000 directory (running as root?)")
	}
	for i := 0; i < 2; i++ {
		res := a.sync(t)
		if n := countDeleteClass(res.Plan); n != 0 {
			t.Fatalf("cycle %d: %d delete-class actions: %+v", i, n, res.Plan)
		}
	}
	if err := os.Chmod(filepath.Join(a.dir, "DOC", "S"), 0o755); err != nil {
		t.Fatal(err)
	}
	s038Settled(t, fake, root, trashed, map[string]string{"DOC/x.txt": "x", "DOC/y.txt": "y", "DOC/S/s.txt": "s"}, "DOC", dID, a, b)
}

// S038.28 — engine fuzz crash=1 ×200 seed 197, minimized (S030's file arm):
// a renames a.txt → A.txt (case only) while Drive edits it; a's plan renames
// the Drive file and downloads the edit; the download fails once. The rename's
// commit must not record Drive's new bytes as synced: the next cycle still
// downloads them. Was: the move's commit stamped Drive's md5 on the row, the
// download never re-planned — a kept the old bytes, silently, at a fixed point.
func TestS038CaseOnlyFileRenameDownloadLostKeepsTheEdit(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	if !names.CaseInsensitiveFS(a.dir) {
		t.Skip("sync dir is on a case-sensitive filesystem")
	}
	a.write(t, "a.txt", "old")
	settleAll(t, a, b)
	b.write(t, "a.txt", "edited by b")
	b.sync(t)
	a.rename(t, "a.txt", "A.txt")
	failOnce(t, executor.CPDownloadTempWritten)
	syncFailing(t, a)
	noFault()
	settleAll(t, a, b)
	wantTrees(t, fake, root, map[string]string{"A.txt": "edited by b"}, "", a, b)
}

// s038RowsAreDrive: m's baseline rows are exactly Drive's tree — every item at
// its Drive path with its id, no row at a path Drive does not hold.
func s038RowsAreDrive(t *testing.T, fake *driveclient.Fake, root string, m *machine) {
	t.Helper()
	drive := map[string]string{}
	var walk func(parentID, prefix string)
	walk = func(parentID, prefix string) {
		children, err := fake.List(context.Background(), parentID)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range children {
			rel := prefix + c.Name
			drive[rel] = c.ID
			if c.IsDir() {
				walk(c.ID, rel+"/")
			}
		}
	}
	walk(root, "")
	rows := map[string]string{}
	for _, it := range mustItems(t, m) {
		rows[it.RelPath] = it.DriveFileID
	}
	for p, id := range rows {
		if drive[p] != id {
			t.Errorf("[%s] row %s (id %s): Drive holds %q there", m.name, p, id, drive[p])
		}
	}
	for p, id := range drive {
		if rows[p] != id {
			t.Errorf("[%s] Drive's %s (id %s) has no row there", m.name, p, id)
		}
	}
}

// S038.31 — row paths around a case-only folder rename here (round 2, F1): a
// renames D → d while Drive renamed the inner D/S (to D/s, or D/T), or while b
// added D/S/s.txt. After a's first cycle its rows are exactly Drive's tree, the
// next cycle plans nothing. Was: the inner move ran as move_local d/S → D/s
// with the row at D/S, and RenameItemPath's LIKE (ASCII case-insensitive)
// re-homed D/S/s.txt at d/s/D/S/s.txt, a path nowhere; a mkdir_local d/S
// committed before move_remote D → d became d/d/S.
func TestS038RowsAroundACaseOnlyFolderRenameHere(t *testing.T) {
	for _, v := range []string{"drive renames D/S to D/s", "drive renames D/S to D/T", "b adds D/S/s.txt"} {
		t.Run(v, func(t *testing.T) {
			fake, root, a, b, trashed := s038Pair(t)
			want := map[string]string{"d/x.txt": "x", "d/y.txt": "y"}
			switch v {
			case "drive renames D/S to D/s":
				a.write(t, "D/S/s.txt", "s")
				settleAll(t, a, b)
				b.rename(t, "D/S", "D/s")
				want["d/s/s.txt"] = "s"
			case "drive renames D/S to D/T":
				a.write(t, "D/S/s.txt", "s")
				settleAll(t, a, b)
				b.rename(t, "D/S", "D/T")
				want["d/T/s.txt"] = "s"
			default:
				b.write(t, "D/S/s.txt", "s")
				want["d/S/s.txt"] = "s"
			}
			b.sync(t)
			trashed = fake.TrashCount()
			a.rename(t, "D", "d")
			a.sync(t)
			s038RowsAreDrive(t, fake, root, a)
			if res := a.sync(t); len(res.Plan) != 0 {
				t.Fatalf("[a] second cycle not idle: %+v", res.Plan)
			}
			settleAll(t, a, b)
			wantTrees(t, fake, root, want, "", a, b)
			s038RowsAreDrive(t, fake, root, a)
			s038RowsAreDrive(t, fake, root, b)
			s020BinsEmpty(t, fake, trashed, a, b)
		})
	}
}

// S038.32 — a deletion made right after such a cycle stays a deletion (F1
// (iii)): with a row at a stale path, the file deleted here read as "deleted
// here, moved on Drive" and Drive's copy came back. Now: binned on Drive, gone
// on b.
func TestS038DeletionAfterACaseOnlyFolderRenameSticks(t *testing.T) {
	for _, v := range []string{"b adds D/S/s.txt", "drive renames D/S to D/s"} {
		t.Run(v, func(t *testing.T) {
			fake, root, a, b, _ := s038Pair(t)
			gone := "d/S/s.txt"
			if v == "b adds D/S/s.txt" {
				b.write(t, "D/S/s.txt", "s")
			} else {
				a.write(t, "D/S/s.txt", "s")
				settleAll(t, a, b)
				b.rename(t, "D/S", "D/s")
				gone = "d/s/s.txt"
			}
			b.sync(t)
			a.rename(t, "D", "d")
			a.sync(t)
			trashed := fake.TrashCount()
			a.remove(t, gone)
			settleAll(t, a, b)
			wantTrees(t, fake, root, map[string]string{"d/x.txt": "x", "d/y.txt": "y"}, "", a, b)
			if got := fake.TrashCount() - trashed; got != 1 {
				t.Errorf("Drive's bin gained %d entries, want 1 (s.txt)", got)
			}
		})
	}
}

// s038SettleClean: settleAll, and no cycle reports a name conflict.
func s038SettleClean(t *testing.T, ms ...*machine) {
	t.Helper()
	for round := 0; round < 6; round++ {
		busy := 0
		for _, m := range ms {
			res := m.sync(t)
			for _, s := range res.Skips {
				if s.Kind == reconcile.SkipNameConflict {
					t.Fatalf("[%s] round %d: spurious name conflict %+v", m.name, round, s)
				}
			}
			busy += len(res.Plan)
		}
		if busy == 0 {
			return
		}
	}
	t.Fatal("did not settle within 6 rounds")
}

// S038.33 — nested renames, one side in case (round 2, F2): the outer folder
// renamed in case here, the inner on Drive (in case or not), new files on
// either side; and the mirror (inner here, outer on Drive). Every cycle has
// zero failed actions and no name conflict; the rows end exactly Drive's tree.
// Was (round 1): a spurious name_conflict for D/S/s.txt, a move_remote of
// Drive's new file back to the old spelling saved only by its failure ("no
// drive id known for parent folder"), an upload at the folder's pre-move path
// (no such file).
func TestS038NestedRenamesOneInCase(t *testing.T) {
	for _, v := range []struct {
		name string
		op   func(t *testing.T, a, b *machine)
		want map[string]string
	}{
		{"outer here, inner in case on Drive", func(t *testing.T, a, b *machine) {
			b.rename(t, "D/S", "D/s")
			b.sync(t)
			a.rename(t, "D", "d")
		}, map[string]string{"d/s/s.txt": "s"}},
		{"outer here, inner in case on Drive, new files both sides", func(t *testing.T, a, b *machine) {
			b.rename(t, "D/S", "D/s")
			b.write(t, "D/s/new.txt", "nb")
			b.sync(t)
			a.rename(t, "D", "d")
			a.write(t, "d/S/na.txt", "na")
		}, map[string]string{"d/s/s.txt": "s", "d/s/new.txt": "nb", "d/s/na.txt": "na"}},
		{"outer here, inner renamed on Drive, new files both sides", func(t *testing.T, a, b *machine) {
			b.rename(t, "D/S", "D/T")
			b.write(t, "D/T/new.txt", "nb")
			b.sync(t)
			a.rename(t, "D", "d")
			a.write(t, "d/S/na.txt", "na")
		}, map[string]string{"d/T/s.txt": "s", "d/T/new.txt": "nb", "d/T/na.txt": "na"}},
		{"inner here, outer in case on Drive", func(t *testing.T, a, b *machine) {
			b.rename(t, "D", "d")
			b.write(t, "d/S/new.txt", "nb")
			b.sync(t)
			a.rename(t, "D/S", "D/s")
			a.write(t, "D/s/na.txt", "na")
		}, map[string]string{"d/s/s.txt": "s", "d/s/new.txt": "nb", "d/s/na.txt": "na"}},
	} {
		t.Run(v.name, func(t *testing.T) {
			fake, root, a, b, _ := s038Pair(t)
			a.write(t, "D/S/s.txt", "s")
			settleAll(t, a, b)
			trashed := fake.TrashCount()
			v.op(t, a, b)
			s038SettleClean(t, a, b)
			want := map[string]string{"d/x.txt": "x", "d/y.txt": "y"}
			for p, c := range v.want {
				want[p] = c
			}
			wantTrees(t, fake, root, want, "", a, b)
			s038RowsAreDrive(t, fake, root, a)
			s038RowsAreDrive(t, fake, root, b)
			s038NoMint(t, fake, root)
			s020BinsEmpty(t, fake, trashed, a, b)
		})
	}
}

// S038.34 — DEC (coordinator, round 2): renamed in case here (D → d), renamed
// to another name on Drive (D → X) → Drive's name wins: one folder X, D's
// Drive id, its files' ids; nothing duplicated, nothing binned, zero failed.
func TestS038CaseRenameHereLosesToARenameOnDrive(t *testing.T) {
	fake, root, a, b, trashed := s038Pair(t)
	dID, xID := driveIDOf(t, a, "D"), driveIDOf(t, a, "D/x.txt")
	s020Mv(t, fake, dID, root, "X")
	a.rename(t, "D", "d")
	s038SettleClean(t, a, b)
	s038Settled(t, fake, root, trashed, map[string]string{"X/x.txt": "x", "X/y.txt": "y"}, "X", dID, a, b)
	if got := driveIDOf(t, a, "X/x.txt"); got != xID {
		t.Errorf("X/x.txt bound to %s, want %s", got, xID)
	}
	s038RowsAreDrive(t, fake, root, a)
}

// S038.35 — round 4 (H1): a folder held because Drive's fold winner is
// another folder (S038.6), renamed here, goes up as a NEW folder with copies,
// as at HEAD: Drive keeps the held D (its id, name, files) beside d; nothing
// binned over four cycles; zero failed. Round 2's release (one move_remote)
// was taken out of this step: it steered pairing and rewrote the winner's
// files (S038.51, S038.52).
func TestS038HeldFolderRenamedHereGoesUpAsNew(t *testing.T) {
	fake, root, a, b, _ := s038Pair(t)
	dID := driveIDOf(t, a, "D")
	s030Burn(t, fake, root)
	d, err := fake.Mkdir(context.Background(), root, "d")
	if err != nil {
		t.Fatal(err)
	}
	s030Upload(t, fake, d.ID, "z.txt", "z")
	trashed := fake.TrashCount()
	a.sync(t)
	b.sync(t)
	a.rename(t, "D", "D2")
	for i := 0; i < 4; i++ {
		for _, m := range []*machine{a, b} {
			for _, act := range m.sync(t).Plan {
				if act.Type == reconcile.MoveRemote || act.Type == reconcile.TrashRemote || act.Type == reconcile.QuarantineLocal {
					t.Fatalf("[%s] cycle %d planned %s %s", m.name, i, act.Type, act.RelPath)
				}
			}
		}
	}
	s020BinsEmpty(t, fake, trashed, a, b)
	if f, err := fake.Get(context.Background(), dID); err != nil || f.Trashed || f.Name != "D" {
		t.Fatalf("Drive's D = %+v %v, want kept", f, err)
	}
	got := driveTreeOf(t, fake, root)
	for p, c := range map[string]string{"D/x.txt": "x", "D/y.txt": "y", "d/z.txt": "z", "D2/x.txt": "x", "D2/y.txt": "y"} {
		if got[p] != c {
			t.Errorf("Drive %s = %q, want %q (all: %v)", p, got[p], c, got)
		}
	}
}

// S038.40 — round 3 (G1): Drive gains a folder d that LOSES the fold to the
// synced D (D first by id). Only d — and what is under it — is held; D's
// subtree syncs as at HEAD: a's and b's edits and b's new file arrive
// everywhere; the only thing reported is d. Was (round 2): the engine matched
// the loser's skip path d under the fold to D's rows — every row of D held,
// reported "shadowed" every cycle, nothing synced.
func TestS038LosingFoldSiblingHoldsOnlyItself(t *testing.T) {
	fake, root, a, b, trashed := s038Pair(t)
	if _, err := fake.Mkdir(context.Background(), root, "d"); err != nil {
		t.Fatal(err)
	}
	settleAll(t, a, b)
	a.write(t, "D/x.txt", "x by a")
	b.write(t, "D/y.txt", "y by b")
	b.write(t, "D/new.txt", "new by b")
	settleAll(t, a, b)
	wantTrees(t, fake, root, map[string]string{"D/x.txt": "x by a", "D/y.txt": "y by b", "D/new.txt": "new by b"}, "", a, b)
	s020BinsEmpty(t, fake, trashed, a, b)
	for _, m := range []*machine{a, b} {
		for _, s := range m.sync(t).Skips {
			if s.RelPath != "d" {
				t.Errorf("[%s] reports %+v; want only d", m.name, s)
			}
		}
	}
}

// S038.41 — round 3 (G2): b deletes D and makes a new D/z.txt (Drive: a new
// folder D, a new id); a renamed D → d here, with or without an edit of x.txt.
// The old D is gone on Drive: its unchanged files follow it (a's bin), an edit
// here beats the delete. The folder here meets Drive's new D: one folder,
// Drive's id and spelling, z.txt here, the edit uploaded into it; zero failed
// actions. Was (round 2): mkdir_local D refused every cycle ("d sits here under
// that name in another case"); at HEAD Drive got a second folder d and z.txt
// ended in b's bin.
func TestS038FolderRecreatedOnDriveWhileRenamedInCaseHere(t *testing.T) {
	for _, edit := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "edited here"}[edit], func(t *testing.T) {
			fake, root, a, b, _ := s038Pair(t)
			b.remove(t, "D")
			b.sync(t)
			b.write(t, "D/z.txt", "z")
			b.sync(t)
			newID := driveIDOf(t, b, "D")
			a.rename(t, "D", "d")
			want := map[string]string{"D/z.txt": "z"}
			if edit {
				a.write(t, "d/x.txt", "x by a")
				want["D/x.txt"] = "x by a"
			}
			settleAll(t, a, b)
			s038NoMint(t, fake, root)
			wantTrees(t, fake, root, want, "", a, b)
			s038RowsAreDrive(t, fake, root, a)
			for _, m := range []*machine{a, b} {
				if got := driveIDOf(t, m, "D"); got != newID {
					t.Errorf("[%s] D bound to %s, want Drive's new folder %s", m.name, got, newID)
				}
				if dirs := s038Dirs(t, m); !dirs["D"] || dirs["d"] {
					t.Errorf("[%s] folders %v, want D spelled as Drive", m.name, dirs)
				}
			}
		})
	}
}

// S038.42 — the same wedge reached by a crash (G2): Drive deleted D, a renamed
// D → d and edited x.txt — the edit beats the delete: mkdir_remote D, upload
// D/x.txt; the mkdir's commit is lost (mkdir_before_commit, W17's seeding
// adopts Drive's new D next cycle). Settles with zero failed actions, one
// folder D holding the edit, nothing minted.
func TestS038ResurrectedFolderCommitLostWhileRenamedInCase(t *testing.T) {
	fake, root, a, b, _ := s038Pair(t)
	b.remove(t, "D")
	b.sync(t)
	a.rename(t, "D", "d")
	a.write(t, "d/x.txt", "x by a")
	failOnce(t, executor.CPMkdirBeforeCommit)
	syncFailing(t, a)
	noFault()
	settleAll(t, a, b)
	s038NoMint(t, fake, root)
	wantTrees(t, fake, root, map[string]string{"D/x.txt": "x by a"}, "", a, b)
	s038RowsAreDrive(t, fake, root, a)
	s038RowsAreDrive(t, fake, root, b)
}

// S038.43 — round 3 (G3): a held folder (Drive's d wins the fold over a's D)
// DELETED here while a new folder B holds a copy of one of its files: not the
// folder renamed (a held folder is never a pairing source — round 2's release
// was taken out in round 4). As at HEAD: D's Drive copy stays (a deleted held file is never binned, R19), B goes
// up as a new folder; no name conflict on B; nothing in any bin over 4 cycles.
// Was (round 2): move_remote D → B, then trash_remote B/y.txt (Drive's bin
// and b's system bin), and a one-cycle name_conflict B/new.txt.
func TestS038HeldFolderDeletedWithACopyIsNotARename(t *testing.T) {
	fake, root, a, b, _ := s038Pair(t)
	dID := driveIDOf(t, a, "D")
	s030Burn(t, fake, root)
	d, err := fake.Mkdir(context.Background(), root, "d")
	if err != nil {
		t.Fatal(err)
	}
	s030Upload(t, fake, d.ID, "z.txt", "z")
	trashed := fake.TrashCount()
	a.sync(t)
	b.sync(t)
	a.write(t, "B/x.txt", "x")
	a.write(t, "B/new.txt", "new")
	a.remove(t, "D")
	for i := 0; i < 4; i++ {
		for _, m := range []*machine{a, b} {
			res := m.sync(t)
			for _, act := range res.Plan {
				if act.Type == reconcile.MoveRemote || act.Type == reconcile.TrashRemote || act.Type == reconcile.QuarantineLocal {
					t.Fatalf("[%s] cycle %d planned %s %s: %+v", m.name, i, act.Type, act.RelPath, res.Plan)
				}
			}
			for _, s := range res.Skips {
				if s.Kind == reconcile.SkipNameConflict && strings.HasPrefix(s.RelPath, "B") {
					t.Fatalf("[%s] cycle %d: spurious %+v", m.name, i, s)
				}
			}
		}
	}
	s020BinsEmpty(t, fake, trashed, a, b)
	if f, err := fake.Get(context.Background(), dID); err != nil || f.Trashed || f.Name != "D" {
		t.Fatalf("Drive's D = %+v %v, want kept", f, err)
	}
	if got := driveTreeOf(t, fake, root); got["D/x.txt"] != "x" || got["D/y.txt"] != "y" || got["B/x.txt"] != "x" || got["B/new.txt"] != "new" {
		t.Fatalf("Drive = %v, want D kept and B uploaded", got)
	}
}

// S038.44 — round 3 (G3, same family): Drive holds a folder d that LOSES the
// fold to a's synced D; a renames D → d here (case only, onto the other
// folder's exact name). Not renamed on Drive (it would hold d twice): a name
// conflict naming d, nothing minted or merged, zero failed; d's file stays on
// Drive only.
func TestS038CaseRenameOntoAShadowedSiblingIsHeld(t *testing.T) {
	fake, root, a, b, trashed := s038Pair(t)
	dID := driveIDOf(t, a, "D")
	n, err := fake.Mkdir(context.Background(), root, "d")
	if err != nil {
		t.Fatal(err)
	}
	s030Upload(t, fake, n.ID, "z.txt", "z")
	settleAll(t, a, b)
	a.rename(t, "D", "d")
	for i := 0; i < 2; i++ {
		res := a.sync(t)
		for _, act := range res.Plan {
			if act.Type == reconcile.MoveRemote || act.Type == reconcile.MkdirRemote || act.Type == reconcile.Upload {
				t.Fatalf("cycle %d planned %s %s: %+v", i, act.Type, act.RelPath, res.Plan)
			}
		}
		if !s038HeldAs(res, "D", "d", "d") {
			t.Fatalf("cycle %d: no name conflict for D naming d: %+v", i, res.Skips)
		}
	}
	settleAll(t, a, b)
	if f, err := fake.Get(context.Background(), dID); err != nil || f.Name != "D" {
		t.Fatalf("Drive's D = %+v %v, want unchanged", f, err)
	}
	if got := driveTreeOf(t, fake, root); len(got) != 3 || got["d/z.txt"] != "z" || got["D/x.txt"] != "x" {
		t.Fatalf("Drive = %v, want D/x, D/y, d/z", got)
	}
	s020BinsEmpty(t, fake, trashed, a, b)
}

// S038.46 — round 3 (G4): S038.9's hold (Drive renamed D → E, a holds a
// different folder e) is released by renaming one of the two entries the skip
// names — e here or E in Drive — not by renaming D here: D → F here is
// "renamed differently on two sides" (DEC-013 (3)): F goes up as a new folder
// with copies and the hold on E stays while e is there. Renaming e then
// releases it: E arrives with x, y; F keeps its copies; g synced; zero failed,
// no skip left, nothing binned.
func TestS038HoldReleasedOnlyByTheNamedEntries(t *testing.T) {
	fake, root, a, b, trashed := s038Pair(t)
	s020Mv(t, fake, driveIDOf(t, a, "D"), root, "E")
	a.write(t, "e/mine.txt", "mine")
	a.sync(t)
	a.rename(t, "D", "F")
	a.sync(t)
	if res := a.sync(t); !s038HasConflict(res, "E", "e") {
		t.Fatalf("[a] after D → F the hold on E/e should remain: %+v", res.Skips)
	}
	a.rename(t, "e", "g")
	settleAll(t, a, b)
	wantTrees(t, fake, root, map[string]string{"E/x.txt": "x", "E/y.txt": "y", "F/x.txt": "x", "F/y.txt": "y", "g/mine.txt": "mine"}, "", a, b)
	s020BinsEmpty(t, fake, trashed, a, b)
	for _, m := range []*machine{a, b} {
		if res := m.sync(t); len(res.Skips) != 0 {
			t.Errorf("[%s] still reports %+v", m.name, res.Skips)
		}
	}
}

// S038.47 — round 3 (G5): the fuzz oracle's survival check (heldLost) itself.
// (a) a held file here binned although edited (its Drive item gone) is a loss
// — edit beats delete; the same file unchanged is §4.2's deletion. (b) a held
// Drive file trashed while its row's file is still here under a folder
// renamed in case (row D/x.txt, disk d/x.txt) is a loss; with the file really
// gone here it is §4.2's deletion. Round 2's exemptions passed both losses.
func TestS038HeldLostOracle(t *testing.T) {
	sum := func(c string) string { h := md5.Sum([]byte(c)); return hex.EncodeToString(h[:]) }
	held := reconcile.Skip{Kind: reconcile.SkipNameConflict, RelPath: "D/x.txt", DrivePath: "E", LocalPath: "e"}
	// (a)
	for _, tc := range []struct {
		here string
		lost bool
	}{{"x synced", false}, {"x edited here", true}} {
		before := holdState{local: map[string]string{"D/x.txt": tc.here}, drive: map[string]string{}, driveIDs: map[string]string{},
			rows: []statedb.Item{{DriveFileID: "X", RelPath: "D/x.txt", ContentMD5: sum("x synced")}}}
		if got := heldLost(held, before, map[string]string{}, map[string]string{}); (got != "") != tc.lost {
			t.Errorf("(a) %q binned, its Drive item gone: heldLost = %q, want lost=%v", tc.here, got, tc.lost)
		}
	}
	// (b)
	for _, tc := range []struct {
		local map[string]string
		lost  bool
	}{{map[string]string{"d/x.txt": "x"}, true}, {map[string]string{}, false}} {
		before := holdState{local: tc.local, drive: map[string]string{"E/x.txt": "x"}, driveIDs: map[string]string{"E/x.txt": "X"},
			rows: []statedb.Item{{DriveFileID: "X", RelPath: "D/x.txt", ContentMD5: sum("x")}}}
		if got := heldLost(held, before, tc.local, map[string]string{}); (got != "") != tc.lost {
			t.Errorf("(b) Drive's E/x.txt trashed, here %v: heldLost = %q, want lost=%v", tc.local, got, tc.lost)
		}
	}
}

// S038.49 — round 3: the crash S038.48 models, end to end. a renames D → d
// here, b renamed D/S → D/s, added D/s/b.txt, deleted D/y.txt; a adds
// d/S/a.txt, edits d/x.txt. a's case-only move_local D/S → D/s loses its
// commit (move_before_commit). Every later cycle: zero failed actions; settles
// with rows = Drive and every file. Was (round 2): move_remote d/s/b.txt →
// d/S/b.txt, failing ("no drive id known for parent folder").
func TestS038CaseFolderMoveCommitLostWithWorkInside(t *testing.T) {
	fake, root, a, b, _ := s038Pair(t)
	a.write(t, "D/S/s.txt", "s")
	settleAll(t, a, b)
	b.rename(t, "D/S", "D/s")
	b.write(t, "D/s/b.txt", "b")
	b.remove(t, "D/y.txt")
	b.sync(t)
	a.rename(t, "D", "d")
	a.write(t, "d/S/a.txt", "a")
	a.write(t, "d/x.txt", "x by a")
	failOnce(t, executor.CPMoveBeforeCommit)
	syncFailing(t, a)
	noFault()
	settleAll(t, a, b)
	wantTrees(t, fake, root, map[string]string{"d/x.txt": "x by a", "d/s/s.txt": "s", "d/s/b.txt": "b", "d/s/a.txt": "a"}, "", a, b)
	s038RowsAreDrive(t, fake, root, a)
	s038RowsAreDrive(t, fake, root, b)
	s038NoMint(t, fake, root)
}

// S038.51 — round 4 (H1): a held D (Drive's d won) deleted here while an
// UNRELATED folder F is renamed to G, F holding a file with D/x.txt's bytes:
// F → G is one move_remote, F's id kept, nothing binned — as at HEAD. Was
// (round 2–3): the deleted held D/x.txt, a pairing source, took G/f.txt —
// mkdir_remote G, move_remote F/f.txt → G/f.txt, trash_remote F.
func TestS038HeldFolderDeletedDoesNotSteerAnotherRename(t *testing.T) {
	fake, root, a, b, _ := s038Pair(t)
	a.write(t, "F/f.txt", "x")
	settleAll(t, a, b)
	fID := driveIDOf(t, a, "F")
	s030Burn(t, fake, root)
	d, err := fake.Mkdir(context.Background(), root, "d")
	if err != nil {
		t.Fatal(err)
	}
	s030Upload(t, fake, d.ID, "z.txt", "z")
	trashed := fake.TrashCount()
	a.sync(t)
	b.sync(t)
	a.remove(t, "D")
	a.rename(t, "F", "G")
	res := a.sync(t)
	moved := false
	for _, act := range res.Plan {
		switch {
		case act.Type == reconcile.MoveRemote && act.RelPath == "F" && act.NewRelPath == "G" && act.FileID == fID:
			moved = true
		case act.Type == reconcile.TrashRemote || act.Type == reconcile.MkdirRemote || act.Type == reconcile.MoveRemote:
			t.Fatalf("[a] planned %s %s → %s: %+v", act.Type, act.RelPath, act.NewRelPath, res.Plan)
		}
	}
	if !moved {
		t.Fatalf("[a] want move_remote F → G (id kept): %+v", res.Plan)
	}
	settleAll(t, a, b)
	s020BinsEmpty(t, fake, trashed, a, b)
	if got := driveTreeOf(t, fake, root); got["G/f.txt"] != "x" || got["D/x.txt"] != "x" || got["d/z.txt"] != "z" {
		t.Fatalf("Drive = %v, want G/f.txt, the held D kept, d/z.txt", got)
	}
}

// S038.52 — round 4 (H1): the winner (an exact duplicate D, or d) holding
// z.txt arrives in the same cycle a renames its held D → B: the winner's file
// comes down into the winner's folder, B goes up as a new folder — as at HEAD.
// Was (round 2–3): mkdir_local D, move_remote D → B, download B/z.txt — the
// winner's file landed in B, rows B=N, no row for D.
func TestS038HeldRenameSameCycleWinnerArrives(t *testing.T) {
	for _, name := range []string{"D", "d"} {
		t.Run(name, func(t *testing.T) {
			fake, root, a, _, _ := s038Pair(t)
			s030Burn(t, fake, root)
			n, err := fake.Mkdir(context.Background(), root, name)
			if err != nil {
				t.Fatal(err)
			}
			s030Upload(t, fake, n.ID, "z.txt", "z")
			a.rename(t, "D", "B")
			res := a.sync(t)
			for _, act := range res.Plan {
				if act.Type == reconcile.MoveRemote || act.Type == reconcile.Download && act.RelPath != name+"/z.txt" {
					t.Fatalf("[a] planned %s %s: %+v", act.Type, act.RelPath, res.Plan)
				}
			}
			if got := driveIDOf(t, a, name); got != n.ID {
				t.Errorf("[a] row %s bound to %s, want the winner %s", name, got, n.ID)
			}
			if a.read(t, name+"/z.txt") != "z" || a.read(t, "B/x.txt") != "x" {
				t.Errorf("[a] tree %v", a.listTree(t))
			}
		})
	}
}

// S038.53 — round 4 (H2): Drive renamed D → E and made a new D (z.txt); a
// renamed D → d here (± an edit). The folder here follows Drive's rename to E
// (its MoveLocal), the new D comes down fresh: no case fix of d → D (d has
// moved away by then). Zero failed actions. Was (round 3): move_local d → D
// failed once ("no such file or directory") — the fix keyed by the pre-move
// path.
func TestS038SwapRecreateWhileRenamedInCase(t *testing.T) {
	for _, edit := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "edited here"}[edit], func(t *testing.T) {
			fake, root, a, b, trashed := s038Pair(t)
			id1 := driveIDOf(t, a, "D")
			s020Mv(t, fake, id1, root, "E")
			n, err := fake.Mkdir(context.Background(), root, "D")
			if err != nil {
				t.Fatal(err)
			}
			s030Upload(t, fake, n.ID, "z.txt", "z")
			a.rename(t, "D", "d")
			want := map[string]string{"E/x.txt": "x", "E/y.txt": "y", "D/z.txt": "z"}
			if edit {
				a.write(t, "d/x.txt", "x by a")
				want["E/x.txt"] = "x by a"
			}
			settleAll(t, a, b)
			wantTrees(t, fake, root, want, "", a, b)
			s038RowsAreDrive(t, fake, root, a)
			s038NoMint(t, fake, root)
			s020BinsEmpty(t, fake, trashed, a, b)
			if got := driveIDOf(t, a, "E"); got != id1 {
				t.Errorf("E bound to %s, want %s", got, id1)
			}
		})
	}
}

// S038.54 — round 4 (H2): Drive renamed P → Q, deleted Q/D, made a new
// Q/D/z.txt; a renamed P/D → P/d here (± an edit). No mkdir_local refused for
// spelling (the case fix is keyed where the folder is after Drive's folder
// moves); settles. The cycle still has HEAD's own one-time failures at
// pre-move paths (S037), so this asserts only the spelling failure's absence.
// Was (round 3): an extra failed "mkdir_local Q/D: Q/d sits here under that
// name in another case".
func TestS038RecreatedUnderRenamedParentWhileRenamedInCase(t *testing.T) {
	for _, edit := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "edited here"}[edit], func(t *testing.T) {
			fake, root, a, b, _ := s038Pair(t)
			a.rename(t, "D", "P/D")
			settleAll(t, a, b)
			b.rename(t, "P", "Q")
			b.sync(t)
			b.remove(t, "Q/D")
			b.sync(t)
			b.write(t, "Q/D/z.txt", "z")
			b.sync(t)
			a.rename(t, "P/D", "P/d")
			if edit {
				a.write(t, "P/d/x.txt", "x by a")
			}
			for round := 0; round < 8; round++ {
				busy := 0
				for _, m := range []*machine{a, b} {
					res, err := m.eng.Sync(context.Background(), Options{})
					if err != nil {
						t.Fatalf("[%s] sync: %v", m.name, err)
					}
					for _, e := range res.Errors {
						if strings.Contains(e, "sits here under that name") {
							t.Fatalf("[%s] round %d: %s", m.name, round, e)
						}
					}
					busy += len(res.Plan) + res.Failed
				}
				if busy == 0 {
					break
				}
			}
			assertConverged(t, a, b)
			s038NoMint(t, fake, root)
			if got := a.listTree(t); got["Q/D/z.txt"] != "z" {
				t.Errorf("[a] = %v, want Q/D/z.txt", got)
			}
		})
	}
}

// S038.57 — round 4 (found re-running the round-3 crash probes): b edited
// D/x.txt; a renamed D → d and edited d/x.txt (a true conflict); the download
// of b's bytes loses its commit (download_before_commit) while the plan's
// last action, move_remote D → d, runs. Recovery commits the download's row
// at D/x.txt under the folder row d. Every later cycle: zero failed; settles
// with a's copy as a conflict copy, b's bytes at d/x.txt, rows = Drive. Was
// (rounds 2–3): conflict_backup of d/x.txt onto its own copy's name refused,
// and the move_local of the file onto itself, every cycle.
func TestS038DownloadCommitLostUnderACaseRenamedFolder(t *testing.T) {
	fake, root, a, b, _ := s038Pair(t)
	b.write(t, "D/x.txt", "x by b")
	b.sync(t)
	a.rename(t, "D", "d")
	a.write(t, "d/x.txt", "x by a")
	failOnce(t, executor.CPDownloadBeforeCommit)
	syncFailing(t, a)
	noFault()
	settleAll(t, a, b)
	wantTrees(t, fake, root, map[string]string{"d/x.txt": "x by b", "d/y.txt": "y"}, "x by a", a, b)
	s038RowsAreDrive(t, fake, root, a)
	s038RowsAreDrive(t, fake, root, b)
}

// S038.59 — round 5 (J1): b edits D/x.txt; a renames D → d, its download's
// commit is lost (rows: folder d, file D/x.txt); before a's recovery cycle b
// follows the folder rename and renames the file in case (x.txt → X.txt). a's
// recovery cycle carries b's rename to its disk (move_local), never back:
// d/X.txt everywhere with b's bytes, rows = Drive, zero failed after the
// crash cycle. Was (round 4): record d/X.txt with the disk at d/x.txt, next
// cycle move_remote d/X.txt → d/x.txt — b's rename silently reverted.
func TestS038StaleRowThenDriveRenamesTheFileInCase(t *testing.T) {
	fake, root, a, b, trashed := s038Pair(t)
	b.write(t, "D/x.txt", "x by b")
	b.sync(t)
	a.rename(t, "D", "d")
	failOnce(t, executor.CPDownloadBeforeCommit)
	syncFailing(t, a)
	noFault()
	b.sync(t)
	b.rename(t, "d/x.txt", "d/X.txt")
	b.sync(t)
	res := a.sync(t)
	for _, act := range res.Plan {
		if act.Type == reconcile.MoveRemote {
			t.Fatalf("[a] recovery cycle planned %s %s → %s: %+v", act.Type, act.RelPath, act.NewRelPath, res.Plan)
		}
	}
	settleAll(t, a, b)
	wantTrees(t, fake, root, map[string]string{"d/X.txt": "x by b", "d/y.txt": "y"}, "", a, b)
	s038RowsAreDrive(t, fake, root, a)
	s038RowsAreDrive(t, fake, root, b)
	s020BinsEmpty(t, fake, trashed, a, b)
}

// S038.60 — round 5 (J2): Drive holds a's D — Drive's pick — and a folder d
// that lost the fold; a renames D → B here: its own rename (move_remote, D's
// id kept), then d comes down; no copies, nothing binned. MANUAL §6's
// "renaming yours sends it up as a new folder" holds only when Drive's pick is
// the other folder (S038.35).
func TestS038WinnerFolderRenamedHereIsARename(t *testing.T) {
	fake, root, a, b, trashed := s038Pair(t)
	dID := driveIDOf(t, a, "D")
	n, err := fake.Mkdir(context.Background(), root, "d")
	if err != nil {
		t.Fatal(err)
	}
	s030Upload(t, fake, n.ID, "z.txt", "z")
	settleAll(t, a, b)
	a.rename(t, "D", "B")
	settleAll(t, a, b)
	wantTrees(t, fake, root, map[string]string{"B/x.txt": "x", "B/y.txt": "y", "d/z.txt": "z"}, "", a, b)
	s020BinsEmpty(t, fake, trashed, a, b)
	if got := driveIDOf(t, a, "B"); got != dID {
		t.Errorf("B bound to %s, want D's id %s", got, dID)
	}
}
