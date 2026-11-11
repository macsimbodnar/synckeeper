package reconcile

import "testing"

// S014 round 2 — rows an unreadable file or directory hides after a local
// rename. Identity by stat for a file (size and mtime, both), by detected
// rename for a directory; never by name.

func renamedUnreadableFileWorld() Input {
	return withTestIdentity(Input{
		Base:            map[string]BaseItem{"x.txt": baseFile("f1", "m1", 3)}, // mtime 1000
		Local:           map[string]LocalItem{},
		Remote:          map[string]RemoteItem{"x.txt": remFile("f1", "m1", 3, 1)},
		UnscannedLocal:  map[string]string{"y.txt": "file could not be read"},
		UnreadableFiles: map[string]LocalItem{"y.txt": {Size: 3, MtimeNS: 1000}},
	})
}

func TestARenamedUnreadableFileHoldsItsOldRow(t *testing.T) {
	in := renamedUnreadableFileWorld()
	plan, skips := Plan(in)
	if len(plan) != 0 {
		t.Fatalf("want the old row held, got %+v", plan)
	}
	if !reportedPaths(skips)["x.txt"] {
		t.Errorf("x.txt not reported: %+v", skips)
	}
}

// Identity, not coincidence of one field: a different size or a different
// mtime holds nothing; nor does an unreadable file that has its own row.
func TestAnUnreadableFileWithAnotherStatHoldsNothingElse(t *testing.T) {
	for name, mut := range map[string]func(*Input){
		"size differs":  func(in *Input) { in.UnreadableFiles["y.txt"] = LocalItem{Size: 4, MtimeNS: 1000} },
		"mtime differs": func(in *Input) { in.UnreadableFiles["y.txt"] = LocalItem{Size: 3, MtimeNS: 1001} },
		"tracked itself": func(in *Input) {
			in.Base["y.txt"] = baseFile("f2", "m2", 3)
			in.Remote["y.txt"] = remFile("f2", "m2", 3, 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := renamedUnreadableFileWorld()
			mut(&in)
			plan, _ := Plan(in)
			trashed := false
			for _, a := range plan {
				if a.Type == TrashRemote && a.RelPath == "x.txt" {
					trashed = true
				}
			}
			if !trashed {
				t.Fatalf("a real deletion of x.txt was held: %+v", plan)
			}
		})
	}
}

// Folder form: d/x.txt, `mv d e`, e/x.txt unreadable and alone. The row is
// held by stat, and folder d is not trashed with it inside.
func TestAFolderRenamedAroundAnUnreadableFileIsNotTrashed(t *testing.T) {
	in := withTestIdentity(Input{
		Base: map[string]BaseItem{
			"d":       baseDir("d1"),
			"d/x.txt": baseFile("f1", "m1", 3),
		},
		Local: map[string]LocalItem{"e": locDir()},
		Remote: map[string]RemoteItem{
			"d":       remDir("d1", 1),
			"d/x.txt": remFile("f1", "m1", 3, 1),
		},
		UnscannedLocal:  map[string]string{"e/x.txt": "file could not be read"},
		UnreadableFiles: map[string]LocalItem{"e/x.txt": {Size: 3, MtimeNS: 1000}},
	})
	plan, skips := Plan(in)
	if n := deleteClassCount(plan); n != 0 {
		t.Fatalf("planned %d deletes: %+v", n, plan)
	}
	if !reportedPaths(skips)["d"] {
		t.Errorf("folder d not reported: %+v", skips)
	}
}

// W18-G after a detected parent rename: p → q (paired through q/a.txt),
// q/sub unreadable. The rows under p/sub are held at q/sub.
func TestAnUnreadableSubdirIsHeldWhereAParentRenamePutsIt(t *testing.T) {
	in := withTestIdentity(Input{
		Base: map[string]BaseItem{
			"p":           baseDir("d1"),
			"p/a.txt":     baseFile("f1", "m1", 3),
			"p/sub":       baseDir("d2"),
			"p/sub/b.txt": baseFile("f2", "m2", 4),
		},
		Local: map[string]LocalItem{
			"q": locDir(), "q/a.txt": locFile("m1", 3), "q/sub": locDir(),
		},
		Remote: map[string]RemoteItem{
			"p":           remDir("d1", 1),
			"p/a.txt":     remFile("f1", "m1", 3, 1),
			"p/sub":       remDir("d2", 1),
			"p/sub/b.txt": remFile("f2", "m2", 4, 1),
		},
		UnreadableDirs: map[string]bool{"q/sub": true},
	})
	plan, skips := Plan(in)
	if len(plan) != 1 || plan[0].Type != MoveRemote || plan[0].RelPath != "p" || plan[0].NewRelPath != "q" {
		t.Fatalf("want the folder rename alone, got %+v", plan)
	}
	if !reportedPaths(skips)["p/sub/b.txt"] {
		t.Errorf("p/sub/b.txt not reported: %+v", skips)
	}
}

// S013-B's guard covers W18-G rows too: another machine moves a file from
// under the locked folder into X; this machine deletes X. X is not trashed
// with that file in it.
func TestAFolderHoldingAnUnreadableRowsDriveItemIsNotTrashed(t *testing.T) {
	in := withTestIdentity(Input{
		Base: map[string]BaseItem{
			"locked":       baseDir("d1"),
			"locked/a.txt": baseFile("f1", "m1", 3),
			"X":            baseDir("d2"),
		},
		Local: map[string]LocalItem{"locked": locDir()},
		Remote: map[string]RemoteItem{
			"locked":  remDir("d1", 1),
			"X":       remDir("d2", 1),
			"X/a.txt": remFile("f1", "m1", 3, 2),
		},
		UnreadableLocal: map[string]bool{"f1": true},
		UnreadableDirs:  map[string]bool{"locked": true},
	})
	plan, _ := Plan(in)
	for _, a := range plan {
		if a.Type == TrashRemote && a.RelPath == "X" {
			t.Fatalf("X trashed with a held row's item inside: %+v", plan)
		}
	}
}

// Accepted residual, pinned so MANUAL §8 stays true: the folder holding an
// unreadable subdirectory renamed with nothing readable beside it, so the
// rename is not detected and the rows read as deleted.
func TestAnUnreadableSubdirUnderAnUndetectedRenameIsADeletion(t *testing.T) {
	in := withTestIdentity(Input{
		Base: map[string]BaseItem{
			"p":           baseDir("d1"),
			"p/sub":       baseDir("d2"),
			"p/sub/b.txt": baseFile("f2", "m2", 4),
		},
		Local: map[string]LocalItem{"q": locDir(), "q/sub": locDir()},
		Remote: map[string]RemoteItem{
			"p":           remDir("d1", 1),
			"p/sub":       remDir("d2", 1),
			"p/sub/b.txt": remFile("f2", "m2", 4, 1),
		},
		UnreadableDirs: map[string]bool{"q/sub": true},
	})
	if plan, _ := Plan(in); deleteClassCount(plan) == 0 {
		t.Fatalf("the documented residual changed (now %+v): update MANUAL §8 and specs §5", plan)
	}
}
