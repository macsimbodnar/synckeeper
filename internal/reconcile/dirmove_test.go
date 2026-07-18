package reconcile

// R9 (spec §4.3, W1.8.2): local directory renames collapse to one remote
// move — the Drive folder keeps its identity, and the plan contains no
// delete-class action. Pins the decided pairing rule and its deliberate
// conservatism (empty dirs and scatters do NOT collapse).

import "testing"

func TestR9LocalDirRenameIsOneRemoteMove(t *testing.T) {
	runPlan(t, Input{
		Base: map[string]BaseItem{
			"Docs":         baseDir("d1"),
			"Docs/one.txt": baseFile("f1", "m1", 3),
			"Docs/two.txt": baseFile("f2", "m2", 4),
		},
		Local: map[string]LocalItem{
			"Papers":         locDir(),
			"Papers/one.txt": locFile("m1", 3),
			"Papers/two.txt": locFile("m2", 4),
		},
		Remote: map[string]RemoteItem{
			"Docs":         remDir("d1", 1),
			"Docs/one.txt": remFile("f1", "m1", 3, 1),
			"Docs/two.txt": remFile("f2", "m2", 4, 1),
		},
	}, []step{{t: MoveRemote, rel: "Docs", newRel: "Papers", fileID: "d1"}})
}

func TestR9NestedTreeRenameCollapsesToOneMove(t *testing.T) {
	runPlan(t, Input{
		Base: map[string]BaseItem{
			"Docs":             baseDir("d1"),
			"Docs/one.txt":     baseFile("f1", "m1", 3),
			"Docs/Sub":         baseDir("d2"),
			"Docs/Sub/two.txt": baseFile("f2", "m2", 4),
		},
		Local: map[string]LocalItem{
			"Papers":             locDir(),
			"Papers/one.txt":     locFile("m1", 3),
			"Papers/Sub":         locDir(),
			"Papers/Sub/two.txt": locFile("m2", 4),
		},
		Remote: map[string]RemoteItem{
			"Docs":             remDir("d1", 1),
			"Docs/one.txt":     remFile("f1", "m1", 3, 1),
			"Docs/Sub":         remDir("d2", 1),
			"Docs/Sub/two.txt": remFile("f2", "m2", 4, 1),
		},
	}, []step{{t: MoveRemote, rel: "Docs", newRel: "Papers", fileID: "d1"}})
}

// An empty directory rename pairs to nothing and stays delete + create —
// intended, not a bug: with no surviving children there is no evidence, and
// inventing a move from ambiguity risks reparenting a subtree the user
// never touched (decisions.md 2026-07-18 "W1.8.2").
func TestR9EmptyDirRenameStaysDeleteCreate(t *testing.T) {
	runPlan(t, Input{
		Base:   map[string]BaseItem{"Docs": baseDir("d1")},
		Local:  map[string]LocalItem{"Papers": locDir()},
		Remote: map[string]RemoteItem{"Docs": remDir("d1", 1)},
	}, []step{
		{t: MkdirRemote, rel: "Papers"},
		{t: TrashRemote, rel: "Docs", fileID: "d1"},
	})
}

// Children landing in two different places is a scatter, not a rename —
// the collapse must not fire; per-file moves and the dir delete stand.
func TestR9ScatterDoesNotCollapse(t *testing.T) {
	runPlan(t, Input{
		Base: map[string]BaseItem{
			"Docs":       baseDir("d1"),
			"Docs/a.txt": baseFile("f1", "m1", 3),
			"Docs/b.txt": baseFile("f2", "m2", 4),
		},
		Local: map[string]LocalItem{
			"X":       locDir(),
			"X/a.txt": locFile("m1", 3),
			"Y":       locDir(),
			"Y/b.txt": locFile("m2", 4),
		},
		Remote: map[string]RemoteItem{
			"Docs":       remDir("d1", 1),
			"Docs/a.txt": remFile("f1", "m1", 3, 1),
			"Docs/b.txt": remFile("f2", "m2", 4, 1),
		},
	}, []step{
		{t: MkdirRemote, rel: "X"},
		{t: MkdirRemote, rel: "Y"},
		{t: MoveRemote, rel: "Docs/a.txt", newRel: "X/a.txt", fileID: "f1"},
		{t: MoveRemote, rel: "Docs/b.txt", newRel: "Y/b.txt", fileID: "f2"},
		{t: TrashRemote, rel: "Docs", fileID: "d1"},
	})
}

// A child edited remotely while the dir was renamed locally does not block
// the collapse, and its download resolves under the NEW name — never a
// zombie source dir, never a duplicate upload (decided at the 2026-07-18
// plan review: rewrite applies to the side that hasn't moved yet).
func TestR9RemoteEditUnderLocallyRenamedDir(t *testing.T) {
	runPlan(t, Input{
		Base: map[string]BaseItem{
			"Docs":         baseDir("d1"),
			"Docs/one.txt": baseFile("f1", "m1", 3),
			"Docs/two.txt": baseFile("f2", "m2", 4),
		},
		Local: map[string]LocalItem{
			"Papers":         locDir(),
			"Papers/one.txt": locFile("m1", 3),
			"Papers/two.txt": locFile("m2", 4),
		},
		Remote: map[string]RemoteItem{
			"Docs":         remDir("d1", 1),
			"Docs/one.txt": remFile("f1", "m1-v2", 5, 2),
			"Docs/two.txt": remFile("f2", "m2", 4, 1),
		},
	}, []step{
		{t: MoveRemote, rel: "Docs", newRel: "Papers", fileID: "d1"},
		{t: Download, rel: "Papers/one.txt", fileID: "f1"},
	})
}

// A brand-new remote child under the locally renamed dir downloads under
// the NEW name too.
func TestR9RemoteNewChildUnderLocallyRenamedDir(t *testing.T) {
	runPlan(t, Input{
		Base: map[string]BaseItem{
			"Docs":         baseDir("d1"),
			"Docs/one.txt": baseFile("f1", "m1", 3),
		},
		Local: map[string]LocalItem{
			"Papers":         locDir(),
			"Papers/one.txt": locFile("m1", 3),
		},
		Remote: map[string]RemoteItem{
			"Docs":         remDir("d1", 1),
			"Docs/one.txt": remFile("f1", "m1", 3, 1),
			"Docs/new.txt": remFile("f9", "m9", 7, 1),
		},
	}, []step{
		{t: MoveRemote, rel: "Docs", newRel: "Papers", fileID: "d1"},
		{t: Download, rel: "Papers/new.txt", fileID: "f9"},
	})
}

// R11 (spec §4.2, A4): rows are decided at the path the item will occupy
// when the action runs. A new local file inside a remotely-moved directory
// must meet its new remote counterpart at the POST-move path — the
// both-new conflict fires, one action per rel_path.
func TestR11NewLocalFileUnderRemotelyMovedDirConflicts(t *testing.T) {
	runPlan(t, Input{
		Base: map[string]BaseItem{"A": baseDir("d1")},
		Local: map[string]LocalItem{
			"A":   locDir(),
			"A/x": locFile("m-local", 7),
		},
		Remote: map[string]RemoteItem{
			"B":   remDir("d1", 2),
			"B/x": remFile("f2", "m-remote", 9, 1),
		},
	}, []step{
		{t: MoveLocal, rel: "A", newRel: "B"},
		{t: ConflictBackup, rel: "B/x", newRel: "B/x" + conflictSuffix},
		{t: Download, rel: "B/x", fileID: "f2"},
		{t: Upload, rel: "B/x" + conflictSuffix},
	})
}

// R11 adopt half: same content on both sides adopts at the post-move path,
// no transfer, no duplicate.
func TestR11NewLocalFileUnderRemotelyMovedDirAdopts(t *testing.T) {
	runPlan(t, Input{
		Base: map[string]BaseItem{"A": baseDir("d1")},
		Local: map[string]LocalItem{
			"A":   locDir(),
			"A/x": locFile("m-same", 7),
		},
		Remote: map[string]RemoteItem{
			"B":   remDir("d1", 2),
			"B/x": remFile("f2", "m-same", 7, 1),
		},
	}, []step{
		{t: MoveLocal, rel: "A", newRel: "B"},
		{t: Record, rel: "B/x", fileID: "f2"},
	})
}
