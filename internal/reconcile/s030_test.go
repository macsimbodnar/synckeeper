package reconcile

import (
	"strings"
	"testing"
)

// S030 (DEC-011) — on a case-folding disk, an action is never planned onto a
// place another file holds here under a name differing only in case: no
// download, no move; and no upload or Drive rename mints a name differing only
// in case from one Drive holds there. The item is a skip of kind
// SkipNameConflict carrying both names; both files stay as they are.

// s030Conflicts returns the name-conflict skips by RelPath.
func s030Conflicts(skips []Skip) map[string]Skip {
	out := map[string]Skip{}
	for _, s := range skips {
		if s.Kind == SkipNameConflict {
			out[s.RelPath] = s
		}
	}
	return out
}

func s030Want(t *testing.T, skips []Skip, rel, drive, local string) {
	t.Helper()
	s, ok := s030Conflicts(skips)[rel]
	if !ok {
		t.Fatalf("no name-conflict skip for %s: %+v", rel, skips)
	}
	if s.DrivePath != drive || s.LocalPath != local {
		t.Errorf("%s: names %q / %q, want %q / %q", rel, s.DrivePath, s.LocalPath, drive, local)
	}
	if !strings.Contains(s.Reason, drive) || !strings.Contains(s.Reason, local) ||
		!strings.Contains(s.Reason, "name conflict") && !strings.Contains(s.Reason, "name clash") {
		t.Errorf("%s: reason must say name conflict and name both files: %q", rel, s.Reason)
	}
}

// S030.5 — the step's main shape: Readme.txt synced here (B); Drive's fold
// winner is README.txt (A), B shadowed → nothing planned; README.txt a name
// conflict naming both. Was: download README.txt (LocalExists false).
func TestS030ShadowedRowWinnerIsNotTheFileHere(t *testing.T) {
	in := Input{
		Base:           map[string]BaseItem{"Readme.txt": baseFile("B", "mb", 1)},
		Local:          map[string]LocalItem{"Readme.txt": locFile("mb", 1)},
		Remote:         map[string]RemoteItem{"README.txt": remFile("A", "ma", 1, 1)},
		ShadowedRemote: map[string]bool{"B": true},
		CaseFold:       true,
	}
	runPlan(t, in, []step{})
	_, skips := Plan(in)
	s030Want(t, skips, "README.txt", "README.txt", "Readme.txt")
}

// S030.6 — shape (b): X moved on Drive db/f0.txt → da/f0.txt; a new da/F0.txt
// here → no move, no upload; both reported with both names. Was: move_local
// (refused every cycle) and upload da/F0.txt (a Drive name differing only in
// case).
func TestS030MoveOntoANewFileDifferingInCase(t *testing.T) {
	in := Input{
		Base: map[string]BaseItem{"da": baseDir("DA"), "db": baseDir("DB"), "db/f0.txt": baseFile("X", "mx", 1)},
		Local: map[string]LocalItem{"da": locDir(), "db": locDir(),
			"db/f0.txt": locFile("mx", 1), "da/F0.txt": locFile("mn", 2)},
		Remote:   map[string]RemoteItem{"da": remDir("DA", 1), "db": remDir("DB", 1), "da/f0.txt": remFile("X", "mx", 1, 1)},
		CaseFold: true,
	}
	runPlan(t, in, []step{})
	_, skips := Plan(in)
	s030Want(t, skips, "db/f0.txt", "da/f0.txt", "da/F0.txt")
	s030Want(t, skips, "da/F0.txt", "da/f0.txt", "da/F0.txt")
}

// S030.7 — f.txt gone from its name here, F.txt here (other bytes), f.txt
// edited on Drive. Round 1 held both as a name conflict; round 2 (the
// coordinator's rule: a file here under another case of a row's name, no row
// claiming it, is that row's own file) reads it as f.txt renamed in case and
// edited here while Drive edited it → the rename goes up, then §4.2's true
// conflict at F.txt: the bytes here kept as a conflict copy (uploaded),
// Drive's downloaded. Was (HEAD): download f.txt (refused every cycle), upload
// F.txt (both names on Drive).
func TestS030RestoreOntoANewFileDifferingInCase(t *testing.T) {
	in := Input{
		Base:     map[string]BaseItem{"f.txt": baseFile("X", "mx", 1)},
		Local:    map[string]LocalItem{"F.txt": locFile("mn", 2)},
		Remote:   map[string]RemoteItem{"f.txt": remFile("X", "my", 1, 2)},
		CaseFold: true,
	}
	runPlan(t, in, []step{
		{t: ConflictBackup, rel: "F.txt", newRel: "F" + conflictSuffix + ".txt"},
		{t: MoveRemote, rel: "f.txt", newRel: "F.txt", fileID: "X"},
		{t: Upload, rel: "F" + conflictSuffix + ".txt"},
		{t: Download, rel: "F.txt", fileID: "X"},
	})
}

// S030.8 — a file edited here whose Drive copy is gone, while Drive gained a
// new file under its name in another case → the resurrecting upload would put
// both names on Drive; the download would land on the edit → neither planned.
func TestS030ResurrectBesideANewDriveFileDifferingInCase(t *testing.T) {
	in := Input{
		Base:     map[string]BaseItem{"f.txt": baseFile("X", "mx", 1)},
		Local:    map[string]LocalItem{"f.txt": locFile("me", 2)},
		Remote:   map[string]RemoteItem{"F.txt": remFile("N", "mn", 1, 1)},
		CaseFold: true,
	}
	runPlan(t, in, []step{})
	_, skips := Plan(in)
	s030Want(t, skips, "f.txt", "F.txt", "f.txt")
	s030Want(t, skips, "F.txt", "F.txt", "f.txt")
}

// S030.9 — guards, unchanged by the rule. (1) A case-only rename here is a
// Drive rename of the same item. (2) A case-only rename on Drive is a local
// move of the same file. (3) A file deleted on Drive and unchanged here, a new
// Drive file in another case: the old one is binned; the new one waits a cycle
// (not a name conflict), then lands. (4) No fold: two names, two files.
func TestS030SameItemAndNoFoldUnchanged(t *testing.T) {
	t.Run("case-only rename here", func(t *testing.T) {
		runPlan(t, Input{
			Base:     map[string]BaseItem{"notes.txt": baseFile("X", "mx", 1)},
			Local:    map[string]LocalItem{"Notes.txt": locFile("mx", 1)},
			Remote:   map[string]RemoteItem{"notes.txt": remFile("X", "mx", 1, 1)},
			CaseFold: true,
		}, []step{{t: MoveRemote, rel: "notes.txt", newRel: "Notes.txt"}})
	})
	t.Run("case-only rename on Drive", func(t *testing.T) {
		in := Input{
			Base:     map[string]BaseItem{"notes.txt": baseFile("X", "mx", 1)},
			Local:    map[string]LocalItem{"notes.txt": locFile("mx", 1)},
			Remote:   map[string]RemoteItem{"Notes.txt": remFile("X", "mx", 1, 1)},
			CaseFold: true,
		}
		runPlan(t, in, []step{{t: MoveLocal, rel: "notes.txt", newRel: "Notes.txt"}})
	})
	t.Run("old one binned first", func(t *testing.T) {
		in := Input{
			Base:     map[string]BaseItem{"f.txt": baseFile("X", "mx", 1)},
			Local:    map[string]LocalItem{"f.txt": locFile("mx", 1)},
			Remote:   map[string]RemoteItem{"F.txt": remFile("N", "mn", 1, 1)},
			CaseFold: true,
		}
		runPlan(t, in, []step{{t: QuarantineLocal, rel: "f.txt"}})
		_, skips := Plan(in)
		if c := s030Conflicts(skips); len(c) != 0 {
			t.Fatalf("not a name conflict: %+v", c)
		}
	})
	t.Run("no fold", func(t *testing.T) {
		in := Input{
			Base:   map[string]BaseItem{"da": baseDir("DA"), "db": baseDir("DB"), "db/f0.txt": baseFile("X", "mx", 1)},
			Local:  map[string]LocalItem{"da": locDir(), "db": locDir(), "db/f0.txt": locFile("mx", 1), "da/F0.txt": locFile("mn", 2)},
			Remote: map[string]RemoteItem{"da": remDir("DA", 1), "db": remDir("DB", 1), "da/f0.txt": remFile("X", "mx", 1, 1)},
		}
		runPlan(t, in, []step{{t: MoveLocal, rel: "db/f0.txt", newRel: "da/f0.txt"}, {t: Upload, rel: "da/F0.txt"}})
	})
}

// S030.10 — one kind for files and folders: S020's folder name clash (D
// deleted here, Drive renamed it to E, an unrelated "e" here; DEC-013) types
// every skip of its hold as SkipNameConflict naming E and e. Behaviour as
// S020.32: nothing planned, reasons still say "name clash".
func TestS030FolderNameClashIsTyped(t *testing.T) {
	in := Input{Base: s020Base(), Local: map[string]LocalItem{"e": locDir(), "e/u.txt": locFile("mu", 1)},
		Remote: s020Renamed("E"), CaseFold: true}
	runPlan(t, in, []step{})
	_, skips := Plan(in)
	for _, p := range []string{"D", "D/x.txt", "D/y.txt"} {
		s030Want(t, skips, p, "E", "e")
	}
}

// S030.13 — a file renamed here (D/a.txt → F.txt, its folder deleted) onto a
// name Drive keeps in another case (f.txt, edited on Drive, deleted here) →
// nothing planned: no restore onto F.txt, no Drive rename to F.txt, and the
// renamed file's Drive item is neither binned nor taken with its folder.
func TestS030RenameHereOntoANameDriveKeepsHolds(t *testing.T) {
	in := Input{
		Base:     map[string]BaseItem{"D": baseDir("FD"), "D/a.txt": baseFile("X", "mx", 1), "f.txt": baseFile("Y", "my", 1)},
		Local:    map[string]LocalItem{"F.txt": locFile("mx", 1)},
		Remote:   map[string]RemoteItem{"D": remDir("FD", 1), "D/a.txt": remFile("X", "mx", 1, 1), "f.txt": remFile("Y", "my2", 1, 2)},
		CaseFold: true,
	}
	runPlan(t, in, []step{})
	_, skips := Plan(in)
	s030Want(t, skips, "f.txt", "f.txt", "F.txt")
	s030Want(t, skips, "F.txt", "f.txt", "F.txt")
}

// --- Round 2 ---

// S030.21 — the shadowed row's file renamed here (Readme.txt → Mine.txt, same
// bytes): its Drive item follows (move_remote, pinned to where Drive holds it),
// and README.txt is no longer in conflict: it comes down. Unpaired (no file
// with its bytes here), the row stays held as before.
func TestS030ShadowedRowRenamedHereFollows(t *testing.T) {
	in := Input{
		Base:           map[string]BaseItem{"Readme.txt": baseFile("B", "mb", 1)},
		Local:          map[string]LocalItem{"Mine.txt": locFile("mb", 1)},
		Remote:         map[string]RemoteItem{"README.txt": remFile("A", "ma", 1, 1)},
		ShadowedRemote: map[string]bool{"B": true},
		ShadowedAt:     map[string]ShadowedItem{"B": {Path: "Readme.txt", Item: RemoteItem{FileID: "B", MD5: "mb", Size: 1, Version: 1, ParentID: "R"}}},
		CaseFold:       true,
	}
	runPlan(t, in, []step{
		{t: MoveRemote, rel: "Readme.txt", newRel: "Mine.txt", fileID: "B"},
		{t: Download, rel: "README.txt", fileID: "A"},
	})
	plan, _ := Plan(in)
	if w := plan[0].RemoteWant; w == nil || w.Name != "Readme.txt" || w.ParentID != "R" || w.MD5 != "mb" {
		t.Errorf("move_remote pins %+v, want Readme.txt in R with md5 mb", w)
	}
	in.Local = map[string]LocalItem{}
	runPlan(t, in, []step{{t: Download, rel: "README.txt", fileID: "A"}})
	_, skips := Plan(in)
	if len(skips) != 1 || skips[0].RelPath != "Readme.txt" || skips[0].FileID != "B" {
		t.Errorf("unpaired: want the shadowed row reported, got %+v", skips)
	}
}

// S030.22 — case-only renames are the item's own: renamed on Drive → a
// move_local pinned to the file's own stat (the gate's case-only arm); renamed
// in case differently here and on Drive → the file moves to Drive's name,
// pinned, and the row is re-homed; renamed here, edited on Drive → move_remote
// to the new name, then the download onto it, pinned and ProtectedBy the move.
func TestS030CaseOnlyRenameIsTheItemsOwn(t *testing.T) {
	base := map[string]BaseItem{"a.txt": baseFile("X", "mx", 1)}
	t.Run("on Drive", func(t *testing.T) {
		in := Input{Base: base, Local: map[string]LocalItem{"a.txt": locFile("mx", 1)},
			Remote: map[string]RemoteItem{"A.txt": remFile("X", "mx", 1, 1)}, CaseFold: true}
		runPlan(t, in, []step{{t: MoveLocal, rel: "a.txt", newRel: "A.txt"}})
		if plan, _ := Plan(in); !plan[0].LocalExists || plan[0].LocalSize != 1 || plan[0].LocalMtimeNS != 1000 {
			t.Errorf("case-only move_local not pinned to its own stat: %+v", plan[0])
		}
	})
	t.Run("both sides, differently", func(t *testing.T) {
		in := Input{Base: base, Local: map[string]LocalItem{"A.TXT": locFile("mx", 1)},
			Remote: map[string]RemoteItem{"A.txt": remFile("X", "mx", 1, 1)}, CaseFold: true}
		runPlan(t, in, []step{{t: MoveLocal, rel: "A.TXT", newRel: "A.txt"}, {t: Record, rel: "A.txt", fileID: "X"}})
		plan, skips := Plan(in)
		if !plan[0].LocalExists || plan[1].ProtectedBy != "A.TXT" || len(skips) != 0 {
			t.Errorf("plan %+v skips %+v", plan, skips)
		}
	})
	t.Run("here, edited on Drive", func(t *testing.T) {
		in := Input{Base: base, Local: map[string]LocalItem{"A.txt": locFile("mx", 1)},
			Remote: map[string]RemoteItem{"a.txt": remFile("X", "my", 1, 2)}, CaseFold: true}
		runPlan(t, in, []step{{t: MoveRemote, rel: "a.txt", newRel: "A.txt", fileID: "X"}, {t: Download, rel: "A.txt", fileID: "X"}})
		plan, skips := Plan(in)
		if d := plan[1]; !d.LocalExists || d.ProtectedBy != "a.txt" || len(skips) != 0 {
			t.Errorf("download %+v skips %+v", d, skips)
		}
	})
	t.Run("here, edited here", func(t *testing.T) {
		in := Input{Base: base, Local: map[string]LocalItem{"A.txt": locFile("me", 2)},
			Remote: map[string]RemoteItem{"a.txt": remFile("X", "mx", 1, 1)}, CaseFold: true}
		runPlan(t, in, []step{{t: MoveRemote, rel: "a.txt", newRel: "A.txt", fileID: "X"}, {t: UpdateRemote, rel: "A.txt", fileID: "X"}})
		if plan, _ := Plan(in); plan[1].RemoteWant == nil || plan[1].RemoteWant.Name != "A.txt" || plan[1].ProtectedBy != "a.txt" {
			t.Errorf("update_remote %+v: want pinned to the renamed item, ProtectedBy the move", plan[1])
		}
	})
}

// S030.23 — the conflict inside a folder Drive renamed this cycle (F → G):
// the holder is looked up where it sits now (F/Readme.txt) → no download into
// G, a name conflict naming G/README.txt and G/Readme.txt. Was: download
// G/README.txt (refused once by the gate).
func TestS030ConflictInAFolderRenamedOnDrive(t *testing.T) {
	in := Input{
		Base:           map[string]BaseItem{"F": baseDir("FF"), "F/Readme.txt": baseFile("B", "mb", 1)},
		Local:          map[string]LocalItem{"F": locDir(), "F/Readme.txt": locFile("mb", 1)},
		Remote:         map[string]RemoteItem{"G": remDir("FF", 2), "G/README.txt": remFile("A", "ma", 1, 1)},
		ShadowedRemote: map[string]bool{"B": true},
		CaseFold:       true,
	}
	runPlan(t, in, []step{{t: MoveLocal, rel: "F", newRel: "G"}})
	_, skips := Plan(in)
	s030Want(t, skips, "G/README.txt", "G/README.txt", "G/Readme.txt")
}
