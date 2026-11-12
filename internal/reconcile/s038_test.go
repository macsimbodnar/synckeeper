package reconcile

import (
	"maps"
	"testing"
)

// S038 — folders on a case-folding disk (casefold.go): a folder here at a
// folder's own path under the fold is that folder. These run on any disk: the
// planner is told the disk folds.

// s038Folder: D (id DD) holding x.txt (X) and y.txt (Y), synced.
func s038Folder() map[string]BaseItem {
	return map[string]BaseItem{"D": baseDir("DD"), "D/x.txt": baseFile("X", "mx", 1), "D/y.txt": baseFile("Y", "my", 1)}
}

func s038Local(dir string) map[string]LocalItem {
	return map[string]LocalItem{dir: locDir(), dir + "/x.txt": locFile("mx", 1), dir + "/y.txt": locFile("my", 1)}
}

func s038Remote(dir string) map[string]RemoteItem {
	return map[string]RemoteItem{dir: remDir("DD", 1), dir + "/x.txt": remFile("X", "mx", 1, 1), dir + "/y.txt": remFile("Y", "my", 1, 1)}
}

// S038.10 — the reproduced shape, a's view: renamed D → e here, D → E on
// Drive → one move_local D → E (the disk's e renamed to Drive's E: its commit
// re-homes the rows). Was: move_local D → E, mkdir_remote e, move_remote
// D/x.txt → e/x.txt, D/y.txt → e/y.txt, trash_remote E.
func TestS038PlanRenamedInCaseDifferently(t *testing.T) {
	runPlan(t, Input{Base: s038Folder(), Local: s038Local("e"), Remote: s038Remote("E"), CaseFold: true},
		[]step{{t: MoveLocal, rel: "D", newRel: "E"}})
	// Without evidence (both files edited here) it stays S020's name clash.
	l := s038Local("e")
	l["e/x.txt"], l["e/y.txt"] = locFile("m1", 2), locFile("m2", 2)
	in := Input{Base: s038Folder(), Local: l, Remote: s038Remote("E"), CaseFold: true}
	runPlan(t, in, []step{})
	_, skips := Plan(in)
	s030Want(t, skips, "D", "E", "e")
}

// S038.11 — a case-only folder rename here, Drive's name as synced → the
// folder's own rename: one move_remote D → d (ids kept), also with no file
// to pair (empty folder, or every file edited). Was: mkdir_remote d beside D,
// the rows read as deleted (row D/x.txt, disk d/x.txt).
func TestS038PlanCaseOnlyFolderRenameHere(t *testing.T) {
	runPlan(t, Input{Base: s038Folder(), Local: s038Local("d"), Remote: s038Remote("D"), CaseFold: true},
		[]step{{t: MoveRemote, rel: "D", newRel: "d", fileID: "DD"}})
	runPlan(t, Input{Base: map[string]BaseItem{"D": baseDir("DD")}, Local: map[string]LocalItem{"d": locDir()},
		Remote: map[string]RemoteItem{"D": remDir("DD", 1)}, CaseFold: true},
		[]step{{t: MoveRemote, rel: "D", newRel: "d", fileID: "DD"}})
	l := s038Local("d")
	l["d/x.txt"], l["d/y.txt"] = locFile("m1", 2), locFile("m2", 2)
	// The folder's rename runs last; everything before it in the rows'
	// spelling (round 2: the rows stay exact through the whole plan).
	runPlan(t, Input{Base: s038Folder(), Local: l, Remote: s038Remote("D"), CaseFold: true}, []step{
		{t: UpdateRemote, rel: "D/x.txt", fileID: "X"},
		{t: UpdateRemote, rel: "D/y.txt", fileID: "Y"},
		{t: MoveRemote, rel: "D", newRel: "d", fileID: "DD"},
	})
	// Normalization folds too: "é" composed here, decomposed in Drive.
	runPlan(t, Input{Base: map[string]BaseItem{"café": baseDir("DD")}, Local: map[string]LocalItem{"Café": locDir()},
		Remote: map[string]RemoteItem{"café": remDir("DD", 1)}, CaseFold: true, NormFold: true},
		[]step{{t: MoveRemote, rel: "café", newRel: "Café", fileID: "DD"}})
}

// S038.12 — the outer and the inner folder renamed in case here: both go up in
// one cycle, at the end of the plan, the inner first (round 2; round 1: the
// outer now, the inner a cycle later).
func TestS038PlanNestedCaseRenames(t *testing.T) {
	base := s038Folder()
	base["D/S"], base["D/S/s.txt"] = baseDir("SS"), baseFile("SF", "ms", 1)
	local := s038Local("d")
	local["d/s"], local["d/s/s.txt"] = locDir(), locFile("ms", 1)
	remote := s038Remote("D")
	remote["D/S"], remote["D/S/s.txt"] = remDir("SS", 1), remFile("SF", "ms", 1, 1)
	runPlan(t, Input{Base: base, Local: local, Remote: remote, CaseFold: true}, []step{
		{t: MoveRemote, rel: "D/S", newRel: "D/s", fileID: "SS"},
		{t: MoveRemote, rel: "D", newRel: "d", fileID: "DD"},
	})
	// Had only the outer gone up (rows at d/S, disk d/s, Drive d/S): the inner
	// goes up the next cycle.
	base2 := map[string]BaseItem{}
	remote2 := map[string]RemoteItem{}
	for q, b := range base {
		base2["d"+q[1:]] = b
	}
	for q, r := range remote {
		remote2["d"+q[1:]] = r
	}
	runPlan(t, Input{Base: base2, Local: local, Remote: remote2, CaseFold: true},
		[]step{{t: MoveRemote, rel: "d/S", newRel: "d/s", fileID: "SS"}})
}

// S038.13 — a case-only folder rename on Drive → move_local D → d (the
// executor renames a folder onto its own other spelling); renamed in case
// differently here (Doc → doc here, → DOC on Drive) → Drive's name wins:
// move_local from the row's path, the disk's spelling renamed to Drive's.
func TestS038PlanCaseOnlyFolderRenameOnDrive(t *testing.T) {
	runPlan(t, Input{Base: s038Folder(), Local: s038Local("D"), Remote: s038Remote("d"), CaseFold: true},
		[]step{{t: MoveLocal, rel: "D", newRel: "d"}})
	doc := map[string]BaseItem{}
	for q, b := range s038Folder() {
		doc["Doc"+q[1:]] = b
	}
	runPlan(t, Input{Base: doc, Local: s038Local("doc"), Remote: s038Remote("DOC"), CaseFold: true},
		[]step{{t: MoveLocal, rel: "Doc", newRel: "DOC"}})
	// Drive also edited a file and added one: met at the folder's new name.
	r := s038Remote("DOC")
	r["DOC/x.txt"], r["DOC/z.txt"] = remFile("X", "mx2", 2, 2), remFile("Z", "mz", 1, 1)
	runPlan(t, Input{Base: doc, Local: s038Local("doc"), Remote: r, CaseFold: true}, []step{
		{t: MoveLocal, rel: "Doc", newRel: "DOC"},
		{t: Download, rel: "DOC/x.txt", fileID: "X"},
		{t: Download, rel: "DOC/z.txt", fileID: "Z"},
	})
	// Renamed in case here only, edited on Drive: the rename goes up, the edit
	// comes down at the new name.
	r = s038Remote("Doc")
	r["Doc/x.txt"] = remFile("X", "mx2", 2, 2)
	runPlan(t, Input{Base: doc, Local: s038Local("doc"), Remote: r, CaseFold: true}, []step{
		{t: Download, rel: "Doc/x.txt", fileID: "X"},
		{t: MoveRemote, rel: "Doc", newRel: "doc", fileID: "DD"},
	})
}

// S038.14 — two different folders whose names fold together, Drive's fold
// winner a new d over the synced D (shadowed, R24): d and everything in it
// wait as name conflicts naming d and D; nothing lands in D.
func TestS038PlanTwoFoldEqualFoldersOnDrive(t *testing.T) {
	in := Input{Base: s038Folder(), Local: s038Local("D"),
		Remote:         map[string]RemoteItem{"d": remDir("N", 1), "d/z.txt": remFile("Z", "mz", 1, 1)},
		ShadowedRemote: map[string]bool{"DD": true, "X": true, "Y": true}, CaseFold: true}
	runPlan(t, in, []step{})
	_, skips := Plan(in)
	s030Want(t, skips, "d", "d", "D")
	s030Want(t, skips, "d/z.txt", "d", "D")
}

// S038.15 — a folder here, a file in Drive (and the reverse), names differing
// only in case: no mkdir_remote, no upload or download into the other's place;
// each item a name conflict naming the two entries.
func TestS038PlanFolderVersusFile(t *testing.T) {
	in := Input{Local: map[string]LocalItem{"f.txt": locDir(), "f.txt/inner.txt": locFile("mi", 1)},
		Remote: map[string]RemoteItem{"F.txt": remFile("F", "mf", 1, 1)}, CaseFold: true}
	runPlan(t, in, []step{})
	_, skips := Plan(in)
	s030Want(t, skips, "f.txt", "F.txt", "f.txt")
	s030Want(t, skips, "f.txt/inner.txt", "F.txt", "f.txt")
	s030Want(t, skips, "F.txt", "F.txt", "f.txt")

	in = Input{Local: map[string]LocalItem{"f.txt": locFile("mf", 1)},
		Remote: map[string]RemoteItem{"F.txt": remDir("F", 1), "F.txt/inner.txt": remFile("I", "mi", 1, 1)}, CaseFold: true}
	runPlan(t, in, []step{})
	_, skips = Plan(in)
	s030Want(t, skips, "F.txt", "F.txt", "f.txt")
	s030Want(t, skips, "F.txt/inner.txt", "F.txt", "f.txt")
}

// S038.16 — new folders here and in Drive, names differing only in case: one
// folder on this disk — adopted at Drive's spelling (record), the disk renamed
// to it first, contents by §4.2's both-new rows. Was: mkdir_remote e beside E.
func TestS038PlanNewFoldersAdopted(t *testing.T) {
	runPlan(t, Input{
		Local:    map[string]LocalItem{"e": locDir(), "e/a.txt": locFile("ma", 1)},
		Remote:   map[string]RemoteItem{"E": remDir("E", 1), "E/b.txt": remFile("B", "mb", 1, 1)},
		CaseFold: true,
	}, []step{
		{t: MoveLocal, rel: "e", newRel: "E"},
		{t: Record, rel: "E", fileID: "E"},
		{t: Upload, rel: "E/a.txt"},
		{t: Download, rel: "E/b.txt", fileID: "B"},
	})
}

// S038.17 — Drive renamed D onto E while a different folder e sits here: the
// rename cannot land → held: D, its rows, e and Drive's new items under E
// reported as a name conflict naming E and e; nothing planned.
func TestS038PlanDriveRenameOntoAFoldHolder(t *testing.T) {
	l := s038Local("D")
	l["e"], l["e/mine.txt"] = locDir(), locFile("mm", 1)
	r := s038Remote("E")
	r["E/new.txt"] = remFile("N", "mn", 1, 1)
	in := Input{Base: s038Folder(), Local: l, Remote: r, CaseFold: true}
	runPlan(t, in, []step{})
	_, skips := Plan(in)
	for _, rel := range []string{"D", "D/x.txt", "D/y.txt", "E/new.txt"} {
		s030Want(t, skips, rel, "E", "e")
	}
}

// S038.18 — a folder renamed here onto a name Drive holds in another case as
// a different item (a file): the reparent is not planned, nothing binned — D
// and its rows held, the renamed folder kept here, named both.
func TestS038PlanRenameHereOntoADriveFoldSibling(t *testing.T) {
	r := s038Remote("D")
	r["E"] = remFile("F", "mf", 1, 1)
	in := Input{Base: s038Folder(), Local: s038Local("e"), Remote: r, CaseFold: true}
	got, skips := Plan(in)
	for _, a := range got {
		if a.Type == TrashRemote || a.Type == MoveRemote || a.Type == MkdirRemote || a.Type == Upload {
			t.Fatalf("planned %s %s: %+v", a.Type, a.RelPath, got)
		}
	}
	s030Want(t, skips, "D", "E", "e")
}

// S038.19 — case-sensitive disk: the same scanned names are different folders
// and the plan is HEAD's — D renamed differently on the two sides, both kept
// (S020, DEC-013 (3)): E comes back with D's files, e goes up as a new folder.
func TestS038PlanCaseSensitiveUnchanged(t *testing.T) {
	runPlan(t, Input{Base: s038Folder(), Local: s038Local("e"), Remote: s038Remote("E")}, []step{
		{t: MkdirLocal, rel: "E", fileID: "DD"},
		{t: MkdirRemote, rel: "e"},
		{t: Download, rel: "E/x.txt", fileID: "X"},
		{t: Download, rel: "E/y.txt", fileID: "Y"},
		{t: Upload, rel: "e/x.txt"},
		{t: Upload, rel: "e/y.txt"},
	})
	runPlan(t, Input{Base: s038Folder(), Local: s038Local("d"), Remote: s038Remote("D")},
		[]step{{t: MoveRemote, rel: "D", newRel: "d", fileID: "DD"}})
}

// S038.37 — mixed machines, at plan level: the folding machine renames D → d
// in case (one move_remote of the folder); the case-sensitive machine then
// meets Drive's d as a remote folder rename — one move_local D → d, nothing
// else. And a case-sensitive machine's rename D → d (it holds both spellings
// apart) reaches the folding machine as Drive's case-only rename.
func TestS038PlanMixedCaseSensitivity(t *testing.T) {
	runPlan(t, Input{Base: s038Folder(), Local: s038Local("d"), Remote: s038Remote("D"), CaseFold: true},
		[]step{{t: MoveRemote, rel: "D", newRel: "d", fileID: "DD"}})
	runPlan(t, Input{Base: s038Folder(), Local: s038Local("D"), Remote: s038Remote("d")},
		[]step{{t: MoveLocal, rel: "D", newRel: "d"}})
	runPlan(t, Input{Base: s038Folder(), Local: s038Local("d"), Remote: s038Remote("D")},
		[]step{{t: MoveRemote, rel: "D", newRel: "d", fileID: "DD"}})
	runPlan(t, Input{Base: s038Folder(), Local: s038Local("D"), Remote: s038Remote("d"), CaseFold: true},
		[]step{{t: MoveLocal, rel: "D", newRel: "d"}})
}

// S038.48 — round 3: rows of one folder in two spellings (a crash cut a
// case-only folder move_local D/S → D/s after its rename, before its commit,
// while the plan's download into D/s committed its row there): folder row
// d/S, file row d/s/b.txt, disk d/s/b.txt, Drive d/s. The file is the row's
// own, in its folder's other spelling — not a file renamed in case here: the
// folder's move_local alone, no move_remote of b.txt back to the stale
// spelling. Was: move_remote d/s/b.txt → d/S/b.txt, saved only by its failure
// ("no drive id known for parent folder").
func TestS038PlanRowsInTwoSpellingsOfOneFolder(t *testing.T) {
	runPlan(t, Input{
		Base:     map[string]BaseItem{"d": baseDir("DD"), "d/S": baseDir("SS"), "d/s/b.txt": baseFile("B", "mb", 1)},
		Local:    map[string]LocalItem{"d": locDir(), "d/s": locDir(), "d/s/b.txt": locFile("mb", 1)},
		Remote:   map[string]RemoteItem{"d": remDir("DD", 1), "d/s": remDir("SS", 2), "d/s/b.txt": remFile("B", "mb", 1, 1)},
		CaseFold: true,
	}, []step{{t: MoveLocal, rel: "d/S", newRel: "d/s"}})
}

// S038.38 — round 4 (H1): with CaseFold false every plan is HEAD's (14ed882).
// Expected plans written out from HEAD, measured on an export of it: a folder
// held because Drive's other folder took its name (shadowed, an exact
// duplicate here), renamed, deleted, partly copied, beside an unrelated
// folder F renamed to G; and S038's shapes on a disk that does not fold. A
// held folder renamed here goes up as a new folder with copies (round 2's
// release was taken out: it steered pairing — D/x.txt's copy took G/f.txt,
// F's rename became mkdir + move + trash — and rewrote the winner's files
// under the local move). Beyond these, 90,000 inputs of TestPlanFuzz's flat,
// folder and wide generators (seeds 0–29,999) plan byte-identically to HEAD
// (scratch comparison, not in the suite).
func TestS038PlanCaseSensitiveAsAtHEAD(t *testing.T) {
	base := func() map[string]BaseItem {
		return map[string]BaseItem{"D": baseDir("DD"), "D/x.txt": baseFile("X", "mx", 1), "D/y.txt": baseFile("Y", "my", 1),
			"F": baseDir("FF"), "F/f.txt": baseFile("FX", "mx", 1)}
	}
	held := func(local map[string]LocalItem, extraRemote map[string]RemoteItem) Input {
		rem := map[string]RemoteItem{"D": remDir("N", 1), "F": remDir("FF", 1), "F/f.txt": remFile("FX", "mx", 1, 1)}
		maps.Copy(rem, extraRemote)
		return Input{Base: base(), Local: local, Remote: rem,
			ShadowedRemote: map[string]bool{"DD": true, "X": true, "Y": true},
			ShadowedAt:     map[string]ShadowedItem{"DD": {Path: "D", Item: RemoteItem{FileID: "DD", IsDir: true, ParentID: "root"}}}}
	}
	F := map[string]LocalItem{"F": locDir(), "F/f.txt": locFile("mx", 1)}
	with := func(m ...map[string]LocalItem) map[string]LocalItem {
		o := map[string]LocalItem{}
		for _, x := range m {
			maps.Copy(o, x)
		}
		return o
	}
	G := map[string]LocalItem{"G": locDir(), "G/f.txt": locFile("mx", 1)}
	l := s038Local("D")
	l["e"], l["e/mine.txt"] = locDir(), locFile("mm", 1)
	rf := s038Remote("D")
	rf["E"] = remFile("F", "mf", 1, 1)
	ins := map[string]Input{
		"unchanged":                  held(with(s038Local("D"), F), nil),
		"renamed B full":             held(with(s038Local("B"), F), nil),
		"renamed B full + extra":     held(with(s038Local("B"), F, map[string]LocalItem{"B/n.txt": locFile("mn", 2)}), nil),
		"deleted, B partial":         held(with(F, map[string]LocalItem{"B": locDir(), "B/x.txt": locFile("mx", 1)}), nil),
		"deleted D; F renamed G":     held(G, nil),
		"renamed B + edit x":         held(with(F, map[string]LocalItem{"B": locDir(), "B/x.txt": locFile("mq", 1), "B/y.txt": locFile("my", 1)}), nil),
		"renamed B, Drive has B":     held(with(s038Local("B"), F), map[string]RemoteItem{"B": remDir("BB", 1)}),
		"renamed B, winner has z":    held(with(s038Local("B"), F), map[string]RemoteItem{"D/z.txt": remFile("Z", "mz", 1, 1)}),
		"renamed into F/D":           held(with(F, map[string]LocalItem{"F/D": locDir(), "F/D/x.txt": locFile("mx", 1), "F/D/y.txt": locFile("my", 1)}), nil),
		"deleted D":                  held(F, nil),
		"F renamed G, D unchanged":   held(with(s038Local("D"), G), nil),
		"renamed B + F renamed G":    held(with(s038Local("B"), G), nil),
		"s038 local d drive X":       {Base: s038Folder(), Local: s038Local("d"), Remote: s038Remote("X")},
		"s038 local D drive X":       {Base: s038Folder(), Local: s038Local("D"), Remote: s038Remote("X")},
		"s038 local doc drive D":     {Base: s038Folder(), Local: s038Local("doc"), Remote: s038Remote("D")},
		"s038 drive E, local D + e":  {Base: s038Folder(), Local: l, Remote: s038Remote("E")},
		"s038 local e, Drive E file": {Base: s038Folder(), Local: s038Local("e"), Remote: rf},
		"s038 new e vs new E": {Local: map[string]LocalItem{"e": locDir(), "e/a.txt": locFile("ma", 1)},
			Remote: map[string]RemoteItem{"E": remDir("E", 1), "E/b.txt": remFile("B", "mb", 1, 1)}},
		"s038 folder f.txt vs file F.txt": {Local: map[string]LocalItem{"f.txt": locDir(), "f.txt/inner.txt": locFile("mi", 1)},
			Remote: map[string]RemoteItem{"F.txt": remFile("F", "mf", 1, 1)}},
	}
	for _, c := range []struct {
		name string
		want []step
	}{
		{"F renamed G, D unchanged", []step{{t: MkdirLocal, rel: "D", fileID: "N"}, {t: MoveRemote, rel: "F", newRel: "G", fileID: "FF"}}},
		{"deleted D", []step{{t: MkdirLocal, rel: "D", fileID: "N"}}},
		{"deleted D; F renamed G", []step{{t: MkdirLocal, rel: "D", fileID: "N"}, {t: MoveRemote, rel: "F", newRel: "G", fileID: "FF"}}},
		{"deleted, B partial", []step{{t: MkdirRemote, rel: "B"}, {t: MkdirLocal, rel: "D", fileID: "N"}, {t: Upload, rel: "B/x.txt"}}},
		{"renamed B + F renamed G", []step{{t: MkdirRemote, rel: "B"}, {t: MkdirLocal, rel: "D", fileID: "N"}, {t: MkdirRemote, rel: "G"}, {t: MoveRemote, rel: "F/f.txt", newRel: "B/x.txt", fileID: "FX"}, {t: Upload, rel: "B/y.txt"}, {t: Upload, rel: "G/f.txt"}, {t: TrashRemote, rel: "F", fileID: "FF"}}},
		{"renamed B + edit x", []step{{t: MkdirRemote, rel: "B"}, {t: MkdirLocal, rel: "D", fileID: "N"}, {t: Upload, rel: "B/x.txt"}, {t: Upload, rel: "B/y.txt"}}},
		{"renamed B full", []step{{t: MkdirRemote, rel: "B"}, {t: MkdirLocal, rel: "D", fileID: "N"}, {t: Upload, rel: "B/x.txt"}, {t: Upload, rel: "B/y.txt"}}},
		{"renamed B full + extra", []step{{t: MkdirRemote, rel: "B"}, {t: MkdirLocal, rel: "D", fileID: "N"}, {t: Upload, rel: "B/n.txt"}, {t: Upload, rel: "B/x.txt"}, {t: Upload, rel: "B/y.txt"}}},
		{"renamed B, Drive has B", []step{{t: MkdirLocal, rel: "D", fileID: "N"}, {t: Record, rel: "B", fileID: "BB"}, {t: Upload, rel: "B/x.txt"}, {t: Upload, rel: "B/y.txt"}}},
		{"renamed B, winner has z", []step{{t: MkdirRemote, rel: "B"}, {t: MkdirLocal, rel: "D", fileID: "N"}, {t: Upload, rel: "B/x.txt"}, {t: Upload, rel: "B/y.txt"}, {t: Download, rel: "D/z.txt", fileID: "Z"}}},
		{"renamed into F/D", []step{{t: MkdirLocal, rel: "D", fileID: "N"}, {t: MkdirRemote, rel: "F/D"}, {t: Upload, rel: "F/D/x.txt"}, {t: Upload, rel: "F/D/y.txt"}}},
		{"s038 drive E, local D + e", []step{{t: MoveLocal, rel: "D", newRel: "E"}, {t: MkdirRemote, rel: "e"}, {t: Upload, rel: "e/mine.txt"}}},
		{"s038 folder f.txt vs file F.txt", []step{{t: MkdirRemote, rel: "f.txt"}, {t: Download, rel: "F.txt", fileID: "F"}, {t: Upload, rel: "f.txt/inner.txt"}}},
		{"s038 local D drive X", []step{{t: MoveLocal, rel: "D", newRel: "X"}}},
		{"s038 local d drive X", []step{{t: MkdirLocal, rel: "X", fileID: "DD"}, {t: MkdirRemote, rel: "d"}, {t: Download, rel: "X/x.txt", fileID: "X"}, {t: Download, rel: "X/y.txt", fileID: "Y"}, {t: Upload, rel: "d/x.txt"}, {t: Upload, rel: "d/y.txt"}}},
		{"s038 local doc drive D", []step{{t: MoveRemote, rel: "D", newRel: "doc", fileID: "DD"}}},
		{"s038 local e, Drive E file", []step{{t: MoveRemote, rel: "D", newRel: "e", fileID: "DD"}, {t: Download, rel: "E", fileID: "F"}}},
		{"s038 new e vs new E", []step{{t: MkdirLocal, rel: "E", fileID: "E"}, {t: MkdirRemote, rel: "e"}, {t: Download, rel: "E/b.txt", fileID: "B"}, {t: Upload, rel: "e/a.txt"}}},
		{"unchanged", []step{{t: MkdirLocal, rel: "D", fileID: "N"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			in, ok := ins[c.name]
			if !ok {
				t.Fatalf("no input %q", c.name)
			}
			runPlan(t, in, c.want)
		})
	}
}

// S038.56 — round 4: a file row in the folder's old spelling (D/x.txt) under a
// folder row already renamed in case (d): crash recovery committed the
// download's row at the op's path after the plan's own move_remote D → d had
// renamed the folder. Drive and disk both at d/x.txt, same bytes → only a
// record re-homing the row; no move of the file onto itself. Was: move_local
// D/x.txt → d/x.txt with the file read as its own destination's occupant —
// a conflict copy planned, refused, every cycle.
func TestS038PlanFileRowInTheFoldersOldSpelling(t *testing.T) {
	runPlan(t, Input{
		Base:     map[string]BaseItem{"d": baseDir("DD"), "D/x.txt": baseFile("X", "mb", 1), "d/y.txt": baseFile("Y", "my", 1)},
		Local:    map[string]LocalItem{"d": locDir(), "d/x.txt": locFile("mb", 1), "d/y.txt": locFile("my", 1)},
		Remote:   map[string]RemoteItem{"d": remDir("DD", 1), "d/x.txt": remFile("X", "mb", 1, 1), "d/y.txt": remFile("Y", "my", 1, 1)},
		CaseFold: true,
	}, []step{{t: Record, rel: "d/x.txt", fileID: "X"}})
}

// S038.58 — round 5 (J1): the same stale row (D/x.txt under folder row d),
// but Drive also renamed the FILE in case (d/X.txt; another machine, after
// the crash): Drive's rename comes to this disk in the same cycle —
// move_local of the row's path to d/X.txt (pinned: one file, two spellings),
// which also re-homes the row; never a re-home at Drive's name that leaves the
// disk at d/x.txt (read next cycle as a case rename here, sent back to Drive).
func TestS038PlanStaleRowDriveRenamedTheFileInCase(t *testing.T) {
	in := Input{
		Base:     map[string]BaseItem{"d": baseDir("DD"), "D/x.txt": baseFile("X", "mb", 1), "d/y.txt": baseFile("Y", "my", 1)},
		Local:    map[string]LocalItem{"d": locDir(), "d/x.txt": locFile("mb", 1), "d/y.txt": locFile("my", 1)},
		Remote:   map[string]RemoteItem{"d": remDir("DD", 1), "d/X.txt": remFile("X", "mb", 1, 1), "d/y.txt": remFile("Y", "my", 1, 1)},
		CaseFold: true,
	}
	runPlan(t, in, []step{{t: MoveLocal, rel: "D/x.txt", newRel: "d/X.txt", fileID: "X"}})
	got, _ := Plan(in)
	if !got[0].LocalExists {
		t.Errorf("the case-only move must be pinned to the file's own stat: %+v", got[0])
	}
}
