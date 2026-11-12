package reconcile

import (
	"strings"
	"testing"
)

// S020 (DEC-006) — a folder deleted here while Drive renamed or moved it: the
// rename wins. The folder comes back at Drive's path (MkdirLocal re-homes its
// row) and its files are restored there, each "deleted here | moved on Drive
// → download" (§4.2); nothing goes to Drive's bin. Neighbours are decided by
// the rules already there (testing.md S020.*).

// s020Base: D/x.txt, D/y.txt synced.
func s020Base() map[string]BaseItem {
	return map[string]BaseItem{
		"D": baseDir("FD"), "D/x.txt": baseFile("X", "mx", 1), "D/y.txt": baseFile("Y", "my", 1),
	}
}

// s020Renamed: Drive renamed D to dir, files as synced.
func s020Renamed(dir string) map[string]RemoteItem {
	return map[string]RemoteItem{
		dir: remDir("FD", 2), dir + "/x.txt": remFile("X", "mx", 1, 1), dir + "/y.txt": remFile("Y", "my", 1, 1),
	}
}

// S020.7 — the step's shape. Was: move_local D→E (no such directory), the
// files trashed one by one at their old paths, E trashed.
func TestS020FolderDeletedHereRenamedOnDriveIsRestored(t *testing.T) {
	runPlan(t, Input{Base: s020Base(), Local: map[string]LocalItem{}, Remote: s020Renamed("E")}, []step{
		{t: MkdirLocal, rel: "E", fileID: "FD"},
		{t: Download, rel: "E/x.txt", fileID: "X"},
		{t: Download, rel: "E/y.txt", fileID: "Y"},
	})
}

// S020.8 — moved under another folder instead of renamed: the same.
func TestS020FolderDeletedHereMovedOnDriveIsRestored(t *testing.T) {
	base := s020Base()
	base["P"] = baseDir("FP")
	remote := map[string]RemoteItem{"P": remDir("FP", 1), "P/D": remDir("FD", 2),
		"P/D/x.txt": remFile("X", "mx", 1, 1), "P/D/y.txt": remFile("Y", "my", 1, 1)}
	runPlan(t, Input{Base: base, Local: map[string]LocalItem{"P": locDir()}, Remote: remote}, []step{
		{t: MkdirLocal, rel: "P/D", fileID: "FD"},
		{t: Download, rel: "P/D/x.txt", fileID: "X"},
		{t: Download, rel: "P/D/y.txt", fileID: "Y"},
	})
}

// S020.9 — renamed and changed on Drive: x edited (download), z added
// (download), y deleted (gone on both sides: forgotten).
func TestS020FolderDeletedHereRenamedAndChangedOnDrive(t *testing.T) {
	remote := map[string]RemoteItem{"E": remDir("FD", 2),
		"E/x.txt": remFile("X", "mx2", 2, 2), "E/z.txt": remFile("Z", "mz", 1, 1)}
	runPlan(t, Input{Base: s020Base(), Local: map[string]LocalItem{}, Remote: remote}, []step{
		{t: MkdirLocal, rel: "E", fileID: "FD"},
		{t: Download, rel: "E/x.txt", fileID: "X"},
		{t: Download, rel: "E/z.txt", fileID: "Z"},
		{t: Forget, rel: "D/y.txt", fileID: "Y"},
	})
}

// S020.10 — only a file of D deleted here, D still here, Drive renamed D:
// the file itself was not moved (§4.3: its new path is the folder's change),
// so §4.2's "deleted | unchanged" bins it — at the path Drive holds it when
// the trash runs. Unchanged by S020 but for that path.
func TestS020FileDeletedHereInAFolderRenamedOnDriveIsBinned(t *testing.T) {
	runPlan(t, Input{Base: s020Base(), Local: map[string]LocalItem{"D": locDir(), "D/y.txt": locFile("my", 1)},
		Remote: s020Renamed("E")}, []step{
		{t: MoveLocal, rel: "D", newRel: "E"},
		{t: TrashRemote, rel: "E/x.txt", fileID: "X"},
	})
}

// s020Nested: D/a.txt, D/S/x.txt, D/S/y.txt synced.
func s020Nested() map[string]BaseItem {
	return map[string]BaseItem{"D": baseDir("FD"), "D/a.txt": baseFile("A", "ma", 1),
		"D/S": baseDir("FS"), "D/S/x.txt": baseFile("X", "mx", 1), "D/S/y.txt": baseFile("Y", "my", 1)}
}

// S020.11 — D/S deleted here, Drive renamed D to E: S was not renamed itself,
// so its deletion stands — binned at E/S with its files, which the folder
// collapse makes one Drive-bin entry (was: the files at their old paths D/S/…,
// outside the collapse — three entries).
func TestS020SubfolderDeletedHereParentRenamedOnDriveIsBinned(t *testing.T) {
	remote := map[string]RemoteItem{"E": remDir("FD", 2), "E/a.txt": remFile("A", "ma", 1, 1),
		"E/S": remDir("FS", 1), "E/S/x.txt": remFile("X", "mx", 1, 1), "E/S/y.txt": remFile("Y", "my", 1, 1)}
	in := Input{Base: s020Nested(), Local: map[string]LocalItem{"D": locDir(), "D/a.txt": locFile("ma", 1)}, Remote: remote}
	runPlan(t, in, []step{
		{t: MoveLocal, rel: "D", newRel: "E"},
		{t: TrashRemote, rel: "E/S/y.txt", fileID: "Y"},
		{t: TrashRemote, rel: "E/S/x.txt", fileID: "X"},
		{t: TrashRemote, rel: "E/S", fileID: "FS"},
	})
	plan, _ := Plan(in)
	got := CollapseDirDeletes(plan, remote)
	var trashes []string
	for _, a := range got {
		if a.Type == TrashRemote {
			trashes = append(trashes, a.RelPath)
		}
	}
	if len(trashes) != 1 || trashes[0] != "E/S" {
		t.Fatalf("collapsed trashes = %v, want one: E/S", trashes)
	}
}

// S020.12 — D/S deleted here, Drive renamed D/S to D/T: the step's shape one
// level down.
func TestS020SubfolderDeletedHereRenamedOnDriveIsRestored(t *testing.T) {
	remote := map[string]RemoteItem{"D": remDir("FD", 1), "D/a.txt": remFile("A", "ma", 1, 1),
		"D/T": remDir("FS", 2), "D/T/x.txt": remFile("X", "mx", 1, 1), "D/T/y.txt": remFile("Y", "my", 1, 1)}
	runPlan(t, Input{Base: s020Nested(), Local: map[string]LocalItem{"D": locDir(), "D/a.txt": locFile("ma", 1)}, Remote: remote}, []step{
		{t: MkdirLocal, rel: "D/T", fileID: "FS"},
		{t: Download, rel: "D/T/x.txt", fileID: "X"},
		{t: Download, rel: "D/T/y.txt", fileID: "Y"},
	})
}

// S020.13 — all of D deleted here, Drive renamed only D/S to D/T: T comes
// back (renamed), a.txt is binned (unchanged on Drive), and D survives
// because content lands in it (§4.4).
func TestS020FolderDeletedHereInnerRenamedOnDrive(t *testing.T) {
	remote := map[string]RemoteItem{"D": remDir("FD", 1), "D/a.txt": remFile("A", "ma", 1, 1),
		"D/T": remDir("FS", 2), "D/T/x.txt": remFile("X", "mx", 1, 1), "D/T/y.txt": remFile("Y", "my", 1, 1)}
	runPlan(t, Input{Base: s020Nested(), Local: map[string]LocalItem{}, Remote: remote}, []step{
		{t: MkdirLocal, rel: "D/T", fileID: "FS"},
		{t: Download, rel: "D/T/x.txt", fileID: "X"},
		{t: Download, rel: "D/T/y.txt", fileID: "Y"},
		{t: TrashRemote, rel: "D/a.txt", fileID: "A"},
	})
}

// S020.14 — all of D deleted here, Drive renamed D to E: S rides with it.
func TestS020FolderDeletedHereRenamedOnDriveCarriesItsSubfolder(t *testing.T) {
	remote := map[string]RemoteItem{"E": remDir("FD", 2), "E/a.txt": remFile("A", "ma", 1, 1),
		"E/S": remDir("FS", 1), "E/S/x.txt": remFile("X", "mx", 1, 1), "E/S/y.txt": remFile("Y", "my", 1, 1)}
	runPlan(t, Input{Base: s020Nested(), Local: map[string]LocalItem{}, Remote: remote}, []step{
		{t: MkdirLocal, rel: "E", fileID: "FD"},
		{t: MkdirLocal, rel: "E/S", fileID: "FS"},
		{t: Download, rel: "E/S/x.txt", fileID: "X"},
		{t: Download, rel: "E/S/y.txt", fileID: "Y"},
		{t: Download, rel: "E/a.txt", fileID: "A"},
	})
}

// S020.15 — D deleted here and a new folder E made here, holding no file of
// D (no evidence: S002's strangers). The folder's row is committed at E (the
// done move's replay), D's files come down into it, E's own file goes up.
// Was: D's files trashed on Drive.
func TestS020FolderDeletedHereStrangerAtTheNewName(t *testing.T) {
	runPlan(t, Input{Base: s020Base(), Local: map[string]LocalItem{"E": locDir(), "E/u.txt": locFile("mu", 1)},
		Remote: s020Renamed("E")}, []step{
		{t: MoveLocal, rel: "D", newRel: "E"},
		{t: Upload, rel: "E/u.txt"},
		{t: Download, rel: "E/x.txt", fileID: "X"},
		{t: Download, rel: "E/y.txt", fileID: "Y"},
	})
}

// S020.16 — the stranger holds a file under one of D's names with other
// bytes: that row is forgotten first (S002; next cycle a conflict copy), the
// other file comes down.
func TestS020FolderDeletedHereStrangerHoldsAName(t *testing.T) {
	runPlan(t, Input{Base: s020Base(), Local: map[string]LocalItem{"E": locDir(), "E/x.txt": locFile("mq", 1)},
		Remote: s020Renamed("E")}, []step{
		{t: Forget, rel: "D/x.txt", fileID: "X"},
		{t: MoveLocal, rel: "D", newRel: "E"},
		{t: Download, rel: "E/y.txt", fileID: "Y"},
	})
}

// S020.17 — with evidence (a file of D found unchanged in E) the rename is
// this machine's own too, and a file missing from E was deleted after it:
// binned. Unchanged by S020.
func TestS020SameRenameHereThenAFileDeletedIsBinned(t *testing.T) {
	runPlan(t, Input{Base: s020Base(), Local: map[string]LocalItem{"E": locDir(), "E/x.txt": locFile("mx", 1)},
		Remote: s020Renamed("E")}, []step{
		{t: MoveLocal, rel: "D", newRel: "E"},
		{t: TrashRemote, rel: "E/y.txt", fileID: "Y"},
	})
}

// S020.18 — D deleted here; on Drive folder E was deleted and D renamed onto
// its name; E is still here with its file. §4.3's arriving folder: D comes
// back at E (its commit replaces E's row), E's unchanged file is binned.
// Was: move_local D→E failed, x.txt trashed on Drive.
func TestS020FolderDeletedHereRenamedOntoAFolderStillHere(t *testing.T) {
	base := map[string]BaseItem{"D": baseDir("FD"), "D/x.txt": baseFile("X", "mx", 1),
		"E": baseDir("FE"), "E/o.txt": baseFile("O", "mo", 1)}
	runPlan(t, Input{Base: base, Local: map[string]LocalItem{"E": locDir(), "E/o.txt": locFile("mo", 1)},
		Remote: map[string]RemoteItem{"E": remDir("FD", 2), "E/x.txt": remFile("X", "mx", 1, 1)}}, []step{
		{t: MkdirLocal, rel: "E", fileID: "FD"},
		{t: Download, rel: "E/x.txt", fileID: "X"},
		{t: QuarantineLocal, rel: "E/o.txt", fileID: "O"},
	})
}

// S020.19 — renamed differently on the two sides (F here, E on Drive): §4.3's
// rule for a file renamed differently on both sides — both kept — applied to
// D's files: E comes back with them, F goes up as a new folder. Was:
// move_local D→E (failed), the files moved on Drive into a new F, E trashed.
func TestS020FolderRenamedDifferentlyOnBothSidesKeepsBoth(t *testing.T) {
	runPlan(t, Input{Base: s020Base(),
		Local:  map[string]LocalItem{"F": locDir(), "F/x.txt": locFile("mx", 1), "F/y.txt": locFile("my", 1)},
		Remote: s020Renamed("E")}, []step{
		{t: MkdirLocal, rel: "E", fileID: "FD"},
		{t: MkdirRemote, rel: "F"},
		{t: Download, rel: "E/x.txt", fileID: "X"},
		{t: Download, rel: "E/y.txt", fileID: "Y"},
		{t: Upload, rel: "F/x.txt"},
		{t: Upload, rel: "F/y.txt"},
	})
}

// S020.20 — a file here where the folder would come back: a type clash
// (§4.2), reported, both left alone — nothing planned, the file not
// uploaded beside Drive's folder. Was: the file uploaded (a second item E on
// Drive) and D's files trashed.
func TestS020FolderDeletedHereFileAtTheNewNameIsAClash(t *testing.T) {
	remote := s020Renamed("E")
	remote["E/z.txt"] = remFile("Z", "mz", 1, 1)
	in := Input{Base: s020Base(), Local: map[string]LocalItem{"E": locFile("mq", 3)}, Remote: remote}
	runPlan(t, in, []step{})
	_, skips := Plan(in)
	got := map[string]bool{}
	for _, s := range skips {
		if strings.Contains(s.Reason, "type clash") {
			got[s.RelPath] = true
		}
	}
	for _, p := range []string{"D", "D/x.txt", "D/y.txt", "E/z.txt"} {
		if !got[p] {
			t.Errorf("no type-clash skip for %s: %+v", p, skips)
		}
	}
}

// --- Round 2 (S020.32–37) ---

// S020.32 — case-folding disk: D deleted here, an untracked "e" here, Drive
// renamed D to "E" → nothing planned; D, its rows reported (name clash); "e"
// not uploaded (no sibling of E differing only in case).
func TestS020CaseFoldFolderAtTheNewNameHolds(t *testing.T) {
	in := Input{Base: s020Base(), Local: map[string]LocalItem{"e": locDir(), "e/u.txt": locFile("mu", 1)},
		Remote: s020Renamed("E"), CaseFold: true}
	runPlan(t, in, []step{})
	_, skips := Plan(in)
	got := map[string]bool{}
	for _, s := range skips {
		if strings.Contains(s.Reason, "name clash") {
			got[s.RelPath] = true
		}
	}
	for _, p := range []string{"D", "D/x.txt", "D/y.txt"} {
		if !got[p] {
			t.Errorf("no name-clash skip for %s: %+v", p, skips)
		}
	}
}

// S020.33 — the same with evidence (D's file unchanged in "e"): this machine
// renamed it too, the case aside — not a deletion: planned as before S020
// (the files paired into "e" on Drive).
func TestS020CaseFoldSameRenameIsNotADeletion(t *testing.T) {
	in := Input{Base: s020Base(), Local: map[string]LocalItem{"e": locDir(), "e/x.txt": locFile("mx", 1), "e/y.txt": locFile("my", 1)},
		Remote: s020Renamed("E"), CaseFold: true}
	plan, _ := Plan(in)
	for _, a := range plan {
		if a.Type == MkdirLocal || a.Type == Download {
			t.Fatalf("restored a folder this machine renamed: %+v", plan)
		}
	}
}

// S020.34 — a file where the folder was: the folder comes back at E, the file
// is uploaded as new.
func TestS020FileAtTheOldPathIsNew(t *testing.T) {
	runPlan(t, Input{Base: s020Base(), Local: map[string]LocalItem{"D": locFile("md", 6)}, Remote: s020Renamed("E")}, []step{
		{t: MkdirLocal, rel: "E", fileID: "FD"},
		{t: Upload, rel: "D"},
		{t: Download, rel: "E/x.txt", fileID: "X"},
		{t: Download, rel: "E/y.txt", fileID: "Y"},
	})
}

// S020.35 — the merge with D/S deleted here: S's deletion stands, binned at
// N/S (one entry after the collapse); no move of the gone S.
func TestS020MergeSubfolderDeletedHereIsBinned(t *testing.T) {
	base := map[string]BaseItem{"D": baseDir("FD"), "D/a.txt": baseFile("A", "ma", 1),
		"D/S": baseDir("FS"), "D/S/x.txt": baseFile("X", "mx", 1)}
	runPlan(t, Input{Base: base,
		Local: map[string]LocalItem{"D": locDir(), "D/a.txt": locFile("ma", 1), "N": locDir(), "N/n.txt": locFile("mn", 1)},
		Remote: map[string]RemoteItem{"N": remDir("FD", 2), "N/a.txt": remFile("A", "ma", 1, 1),
			"N/S": remDir("FS", 1), "N/S/x.txt": remFile("X", "mx", 1, 1)}}, []step{
		{t: MoveLocal, rel: "D/a.txt", newRel: "N/a.txt"},
		{t: Record, rel: "N", fileID: "FD"},
		{t: Upload, rel: "N/n.txt"},
		{t: TrashRemote, rel: "N/S/x.txt", fileID: "X"},
		{t: TrashRemote, rel: "N/S", fileID: "FS"},
		{t: QuarantineLocal, rel: "D"},
	})
}

// S020.36 — Drive renamed D onto the name of a live folder E, E shadowed
// (two Drive folders named E) → nothing planned; E's row kept.
func TestS020RenamedOntoAShadowedFolderHolds(t *testing.T) {
	base := s020Base()
	base["E"] = baseDir("FE")
	base["E/o.txt"] = baseFile("O", "mo", 1)
	in := Input{Base: base, Local: map[string]LocalItem{"E": locDir(), "E/o.txt": locFile("mo", 1)},
		Remote: s020Renamed("E"), ShadowedRemote: map[string]bool{"FE": true, "O": true}}
	in.Machine, in.Now = "test_box", testNow
	plan, _ := Plan(in)
	for _, a := range plan {
		if a.FileID == "FD" || a.FileID == "X" || a.FileID == "Y" || a.FileID == "FE" {
			t.Fatalf("planned %s %s on a held restore or E's row: %+v", a.Type, a.RelPath, plan)
		}
	}
}

// S020.37 — Drive swapped D and E by id; D deleted here → D's rows forgotten
// first, E moves to D, then D's folder comes back at E (after that move).
func TestS020SwapOnDriveWithOneDeletedHere(t *testing.T) {
	base := s020Base()
	base["E"] = baseDir("FE")
	base["E/o.txt"] = baseFile("O", "mo", 1)
	in := Input{Base: base, Local: map[string]LocalItem{"E": locDir(), "E/o.txt": locFile("mo", 1)},
		Remote: map[string]RemoteItem{"E": remDir("FD", 2), "E/x.txt": remFile("X", "mx", 1, 1), "E/y.txt": remFile("Y", "my", 1, 1),
			"D": remDir("FE", 2), "D/o.txt": remFile("O", "mo", 1, 1)}}
	runPlan(t, in, []step{
		{t: Forget, rel: "D", fileID: "FD"},
		{t: Forget, rel: "D/x.txt", fileID: "X"},
		{t: Forget, rel: "D/y.txt", fileID: "Y"},
		{t: MoveLocal, rel: "E", newRel: "D"},
		{t: MkdirLocal, rel: "E", fileID: "FD"},
		{t: Download, rel: "E/x.txt", fileID: "X"},
		{t: Download, rel: "E/y.txt", fileID: "Y"},
	})
	plan, _ := Plan(in)
	for _, a := range plan {
		if a.Type == MkdirLocal && a.ProtectedBy != "E" || a.Type == MoveLocal && a.ProtectedBy != "D/y.txt" {
			t.Errorf("%s %s ProtectedBy %q", a.Type, a.RelPath, a.ProtectedBy)
		}
	}
}
