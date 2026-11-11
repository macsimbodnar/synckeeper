package reconcile

import "testing"

// S013 (audit 2026-11-11 F07) — a path on disk the scan does not read (a
// symlink, never followed; a non-regular file) is held by path: nothing at or
// under it is decided or written, on either side, and the rows wait for it to
// be a plain file or folder again.
//
// Each case is planned twice, without the hold and with it, so a test cannot
// quietly stop proving anything if the path it guards changes.

// photos was a tracked folder; the user moved it to another disk and left a
// symlink. The scan reports the link and leaves photos out of Local.
func symlinkedFolderWorld() Input {
	return withTestIdentity(Input{
		Base: map[string]BaseItem{
			"keep.txt":          baseFile("f1", "m1", 3),
			"photos":            baseDir("d1"),
			"photos/1.jpg":      baseFile("f2", "m2", 5),
			"photos/deep":       baseDir("d2"),
			"photos/deep/2.jpg": baseFile("f3", "m3", 7),
		},
		Local: map[string]LocalItem{
			"keep.txt": locFile("m1", 3),
		},
		Remote: map[string]RemoteItem{
			"keep.txt":          remFile("f1", "m1", 3, 1),
			"photos":            remDir("d1", 1),
			"photos/1.jpg":      remFile("f2", "m2", 5, 1),
			"photos/deep":       remDir("d2", 1),
			"photos/deep/2.jpg": remFile("f3", "m3", 7, 1),
		},
	})
}

func holdSymlink(in *Input, paths ...string) {
	in.UnscannedLocal = map[string]string{}
	for _, p := range paths {
		in.UnscannedLocal[p] = "symlink; not followed"
	}
}

// touching returns the plan's actions that name p or a path under it.
func touching(plan []Action, p string) []Action {
	var out []Action
	under := func(q string) bool { return q == p || len(q) > len(p) && q[:len(p)+1] == p+"/" }
	for _, a := range plan {
		if under(a.RelPath) || (a.NewRelPath != "" && under(a.NewRelPath)) {
			out = append(out, a)
		}
	}
	return out
}

func reportedPaths(skips []Skip) map[string]bool {
	out := map[string]bool{}
	for _, s := range skips {
		out[s.RelPath] = true
	}
	return out
}

func TestUnscannedFolderIsNeverReadAsALocalDeletion(t *testing.T) {
	in := symlinkedFolderWorld()
	if naive, _ := Plan(in); deleteClassCount(naive) == 0 {
		t.Fatal("precondition failed: an unseen folder must otherwise plan deletes")
	}
	holdSymlink(&in, "photos")
	plan, skips := Plan(in)
	if len(plan) != 0 {
		t.Fatalf("want an empty plan, got %+v", plan)
	}
	got := reportedPaths(skips)
	for _, want := range []string{"photos", "photos/1.jpg", "photos/deep", "photos/deep/2.jpg"} {
		if !got[want] {
			t.Errorf("%s held but not reported: %+v", want, skips)
		}
	}
}

// Drive edits, adds, deletes and creates folders under the held path. Not one
// of those may become a local write (it would land in the link's target,
// outside the sync dir), and a Drive-side deletion must not Forget the row —
// that is what lets it propagate once the link is a folder again.
func TestDriveChangesUnderAnUnscannedFolderAreHeld(t *testing.T) {
	in := symlinkedFolderWorld()
	in.Remote["photos/1.jpg"] = remFile("f2", "m2-edited", 6, 2)
	delete(in.Remote, "photos/deep/2.jpg")
	in.Remote["photos/new.jpg"] = remFile("n1", "m9", 9, 1)
	in.Remote["photos/newdir"] = remDir("n2", 1)

	naive, _ := Plan(in)
	if len(touching(naive, "photos")) == 0 {
		t.Fatal("precondition failed: Drive changes under photos must otherwise be planned")
	}
	holdSymlink(&in, "photos")
	plan, skips := Plan(in)
	if got := touching(plan, "photos"); len(got) != 0 {
		t.Fatalf("Drive changes under a symlinked folder were planned: %+v", got)
	}
	if len(plan) != 0 {
		t.Fatalf("want an empty plan, got %+v", plan)
	}
	got := reportedPaths(skips)
	for _, want := range []string{"photos/new.jpg", "photos/newdir", "photos/deep/2.jpg"} {
		if !got[want] {
			t.Errorf("%s not reported: %+v", want, skips)
		}
	}
}

// Drive moves a file, and a folder, from elsewhere in the tree into the held
// path. The local move would write through the link; both rows wait.
func TestDriveMovesIntoAnUnscannedFolderAreHeld(t *testing.T) {
	in := symlinkedFolderWorld()
	in.Base["docs"] = baseDir("d3")
	in.Base["docs/a.txt"] = baseFile("f4", "m4", 4)
	in.Base["b.txt"] = baseFile("f5", "m5", 5)
	in.Local["docs"] = locDir()
	in.Local["docs/a.txt"] = locFile("m4", 4)
	in.Local["b.txt"] = locFile("m5", 5)
	in.Remote["photos/docs"] = remDir("d3", 2)
	in.Remote["photos/docs/a.txt"] = remFile("f4", "m4", 4, 1)
	in.Remote["photos/b.txt"] = remFile("f5", "m5", 5, 2)

	naive, _ := Plan(withHeldPhotosOnly(in))
	if len(touching(naive, "photos")) == 0 {
		t.Fatal("precondition failed: moves into photos must otherwise be planned")
	}
	holdSymlink(&in, "photos")
	plan, skips := Plan(in)
	if len(plan) != 0 {
		t.Fatalf("a move through a symlinked folder was planned: %+v", plan)
	}
	got := reportedPaths(skips)
	for _, want := range []string{"docs", "docs/a.txt", "b.txt"} {
		if !got[want] {
			t.Errorf("%s not reported: %+v", want, skips)
		}
	}
}

// withHeldPhotosOnly is in, with the photos subtree held by ids the way an
// unreadable directory would be — so the precondition isolates the moves.
func withHeldPhotosOnly(in Input) Input {
	in.UnreadableLocal = map[string]bool{"d1": true, "f2": true, "d2": true, "f3": true}
	return in
}

// A tracked FILE replaced by a symlink or a non-regular file: same rule. Drive
// editing it must not replace the link; Drive deleting it must not forget it.
func TestUnscannedFileIsHeld(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remote map[string]RemoteItem
	}{
		{"unchanged", map[string]RemoteItem{"doc.txt": remFile("f1", "m1", 3, 1)}},
		{"edited on Drive", map[string]RemoteItem{"doc.txt": remFile("f1", "m1b", 4, 2)}},
		{"deleted on Drive", map[string]RemoteItem{}},
		{"replaced on Drive", map[string]RemoteItem{"doc.txt": remFile("f9", "m9", 9, 1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := withTestIdentity(Input{
				Base:   map[string]BaseItem{"doc.txt": baseFile("f1", "m1", 3)},
				Local:  map[string]LocalItem{},
				Remote: tc.remote,
			})
			if naive, _ := Plan(in); len(naive) == 0 {
				t.Fatal("precondition failed: a missing local file must otherwise be planned")
			}
			in.UnscannedLocal = map[string]string{"doc.txt": "not a regular file"}
			plan, skips := Plan(in)
			if len(plan) != 0 {
				t.Fatalf("want an empty plan, got %+v", plan)
			}
			if !reportedPaths(skips)["doc.txt"] {
				t.Errorf("doc.txt not reported: %+v", skips)
			}
		})
	}
}

// The user copied photos into a new real folder and replaced photos itself
// with a symlink. The copy is a new folder, not photos' new home: a
// local-driven move would rename the Drive folder off every other machine's
// photos.
func TestACopyBesideAnUnscannedFolderIsNotItsRename(t *testing.T) {
	in := symlinkedFolderWorld()
	in.Local["copy"] = locDir()
	in.Local["copy/1.jpg"] = locFile("m2", 5)
	in.Local["copy/deep"] = locDir()
	in.Local["copy/deep/2.jpg"] = locFile("m3", 7)

	naive, _ := Plan(in)
	var naiveMoves int
	for _, a := range naive {
		if a.Type == MoveRemote {
			naiveMoves++
		}
	}
	if naiveMoves == 0 {
		t.Fatalf("precondition failed: the copy must otherwise pair as a rename: %+v", naive)
	}
	holdSymlink(&in, "photos")
	plan, _ := Plan(in)
	if got := touching(plan, "photos"); len(got) != 0 {
		t.Fatalf("the symlinked folder was moved or deleted: %+v", got)
	}
	var uploads int
	for _, a := range plan {
		if a.Type == Upload {
			uploads++
		}
	}
	if uploads != 2 {
		t.Fatalf("want the copy uploaded as two new files, got %+v", plan)
	}
}

// Drive deleted a folder that, locally, holds a symlink the scan did not read.
// Removing the folder would take the user's link with it; the folder stays,
// while its ordinary tracked files still follow Drive's deletion.
func TestAFolderHoldingAnUnscannedPathIsNotRemoved(t *testing.T) {
	in := withTestIdentity(Input{
		Base: map[string]BaseItem{
			"media":       baseDir("d1"),
			"media/a.txt": baseFile("f1", "m1", 3),
		},
		Local: map[string]LocalItem{
			"media":       locDir(),
			"media/a.txt": locFile("m1", 3),
		},
		Remote: map[string]RemoteItem{},
	})
	if naive, _ := Plan(in); len(touching(naive, "media")) != 2 {
		t.Fatalf("precondition failed: the folder must otherwise be removed with its file: %+v", naive)
	}
	in.UnscannedLocal = map[string]string{"media/link": "symlink; not followed"}
	plan, skips := Plan(in)
	if len(plan) != 1 || plan[0].Type != QuarantineLocal || plan[0].RelPath != "media/a.txt" {
		t.Fatalf("want only media/a.txt removed, got %+v", plan)
	}
	if !reportedPaths(skips)["media"] {
		t.Errorf("the kept folder was not reported: %+v", skips)
	}
}

// linkInRenamedParentWorld: a/link was already a symlink (held); the user
// renames a to b. The scan sees dir b and the symlink b/link, nothing under
// a. With no sibling file to pair, no local folder rename is detected.
func linkInRenamedParentWorld() Input {
	return withTestIdentity(Input{
		Base: map[string]BaseItem{
			"a":            baseDir("d1"),
			"a/link":       baseDir("d2"),
			"a/link/1.jpg": baseFile("f1", "m1", 3),
			"a/link/2.jpg": baseFile("f2", "m2", 4),
		},
		Local: map[string]LocalItem{"b": locDir()},
		Remote: map[string]RemoteItem{
			"a":            remDir("d1", 1),
			"a/link":       remDir("d2", 1),
			"a/link/1.jpg": remFile("f1", "m1", 3, 1),
			"a/link/2.jpg": remFile("f2", "m2", 4, 1),
		},
		UnscannedLocal: map[string]string{"b/link": "symlink; not followed"},
	})
}

// Accepted residual (round-3 review): the held link's parent renamed with
// nothing beside the link to pair, so no folder rename is detected. The
// rows are at no unscanned path and read as deleted locally — the Drive
// folder is trashed (MANUAL §8). A same-name guess was tried and rejected:
// any unrelated link of that name turned real moves into copies and real
// deletions into partial ones. Pinned so the docs stay true.
func TestAnUndetectedParentRenameOfAHeldLinkIsADeletion(t *testing.T) {
	in := linkInRenamedParentWorld()
	plan, _ := Plan(in)
	if n := deleteClassCount(plan); n == 0 {
		t.Fatalf("the documented residual changed (now %+v): update MANUAL §8 and specs §5", plan)
	}
}

// With a sibling file the rename IS detected and the rows follow it to b/link.
func TestAHeldSubtreeFollowsADetectedParentRename(t *testing.T) {
	in := linkInRenamedParentWorld()
	in.Base["a/x.txt"] = baseFile("f3", "m3", 5)
	in.Remote["a/x.txt"] = remFile("f3", "m3", 5, 1)
	in.Local["b/x.txt"] = locFile("m3", 5)
	plan, _ := Plan(in)
	if n := deleteClassCount(plan); n != 0 {
		t.Fatalf("planned %d deletes: %+v", n, plan)
	}
	if len(plan) != 1 || plan[0].Type != MoveRemote || plan[0].NewRelPath != "b" {
		t.Fatalf("want the folder rename alone, got %+v", plan)
	}
}

// Review hole 2: holds match under the sync dir's name folding.
func TestHoldsHonourNameFolding(t *testing.T) {
	// (a) untracked link "photos", Drive folder "Photos/x.jpg" new.
	in := withTestIdentity(Input{
		Base:  map[string]BaseItem{},
		Local: map[string]LocalItem{},
		Remote: map[string]RemoteItem{
			"Photos":       remDir("d1", 1),
			"Photos/x.jpg": remFile("f1", "m1", 3, 1),
		},
		UnscannedLocal: map[string]string{"photos": "symlink; not followed"},
		CaseFold:       true,
	})
	if plan, _ := Plan(in); len(plan) != 0 {
		t.Fatalf("Drive content planned through a case-folded symlink: %+v", plan)
	}

	// (b) tracked folder "Photos" replaced by a link spelled "photos".
	in = withTestIdentity(Input{
		Base: map[string]BaseItem{
			"Photos":       baseDir("d1"),
			"Photos/x.jpg": baseFile("f1", "m1", 3),
		},
		Local: map[string]LocalItem{},
		Remote: map[string]RemoteItem{
			"Photos":       remDir("d1", 1),
			"Photos/x.jpg": remFile("f1", "m1", 3, 1),
		},
		UnscannedLocal: map[string]string{"photos": "symlink; not followed"},
		CaseFold:       true,
	})
	if plan, _ := Plan(in); len(plan) != 0 {
		t.Fatalf("a folder replaced by a case-folded symlink was acted on: %+v", plan)
	}

	// Without folding the two spellings are two paths; the hold must not
	// over-reach (Photos/x.jpg reads as deleted, as it would on Linux ext4).
	in.CaseFold = false
	if plan, _ := Plan(in); deleteClassCount(plan) == 0 {
		t.Fatalf("a non-folding filesystem held a differently spelled path: %+v", plan)
	}
}

// Round-3 review, B: a held row's Drive item moved by another machine into a
// folder this machine then deletes locally. The held row never materialized
// at X/x here, so a folder trash would carry X/x into Drive's bin — and, once
// photos is a folder again, the local copy would follow. Without the hold the
// same world downloads X/x (the move beats the delete) and X survives; with
// it, X must survive too.
func heldItemMovedIntoDeletedFolderWorld() Input {
	return withTestIdentity(Input{
		Base: map[string]BaseItem{
			"photos":   baseDir("d1"),
			"photos/x": baseFile("f1", "m1", 3),
			"X":        baseDir("d2"),
			"X/y":      baseFile("f2", "m2", 4),
		},
		Local: map[string]LocalItem{}, // photos is a symlink; X deleted locally
		Remote: map[string]RemoteItem{
			"photos": remDir("d1", 1),
			"X":      remDir("d2", 1),
			"X/x":    remFile("f1", "m1", 3, 2), // moved on Drive by another machine
			"X/y":    remFile("f2", "m2", 4, 1),
		},
	})
}

func TestAFolderHoldingAHeldRowsDriveItemIsNotTrashed(t *testing.T) {
	in := heldItemMovedIntoDeletedFolderWorld()
	control, _ := Plan(in)
	var downloadsX int
	for _, a := range control {
		if a.Type == Download && a.RelPath == "X/x" {
			downloadsX++
		}
		if a.Type == TrashRemote && a.RelPath == "X" {
			t.Fatalf("precondition failed: without the hold the move must beat the delete: %+v", control)
		}
	}
	if downloadsX != 1 {
		t.Fatalf("precondition failed: without the hold X/x downloads: %+v", control)
	}

	holdSymlink(&in, "photos")
	plan, skips := Plan(in)
	for _, a := range plan {
		if a.RelPath == "X" || a.RelPath == "X/x" || a.FileID == "f1" {
			t.Fatalf("the held row's Drive item was acted on: %+v", plan)
		}
	}
	if len(plan) != 1 || plan[0].Type != TrashRemote || plan[0].RelPath != "X/y" {
		t.Fatalf("want only the user's deletion of X/y, got %+v", plan)
	}
	if !reportedPaths(skips)["X"] {
		t.Errorf("the kept folder was not reported: %+v", skips)
	}
}

// The mirror image: Drive deleted folder docs, but a held row's local file
// still sits inside it (Drive had moved that file into the symlinked
// folder, so the row is held where it is). Binning docs locally would take
// the file with it.
func TestALocalFolderHoldingAHeldRowsFileIsNotRemoved(t *testing.T) {
	in := withTestIdentity(Input{
		Base: map[string]BaseItem{
			"docs":       baseDir("d1"),
			"docs/a.txt": baseFile("f1", "m1", 3),
		},
		Local: map[string]LocalItem{
			"docs":       locDir(),
			"docs/a.txt": locFile("m1", 3),
		},
		Remote: map[string]RemoteItem{
			"photos":       remDir("d9", 1),
			"photos/a.txt": remFile("f1", "m1", 3, 2),
		},
	})
	holdSymlink(&in, "photos")
	plan, skips := Plan(in)
	if len(plan) != 0 {
		t.Fatalf("want nothing planned while a.txt is held, got %+v", plan)
	}
	if !reportedPaths(skips)["docs"] {
		t.Errorf("the kept folder was not reported: %+v", skips)
	}
}

// Round-3 review, A: a symlink holds its own path only. An unrelated link
// that merely shares a name with a moved file or renamed folder changes
// nothing — the move stays one move, never a copy.
func TestAnUnrelatedSameNameLinkHoldsNothingElse(t *testing.T) {
	in := withTestIdentity(Input{
		Base: map[string]BaseItem{
			"notes":           baseDir("d1"),
			"notes/README.md": baseFile("f1", "m1", 3),
			"data":            baseDir("d2"),
			"data/a.bin":      baseFile("f2", "m2", 4),
		},
		Local: map[string]LocalItem{
			"notes":             locDir(),
			"archive":           locDir(),
			"archive/README.md": locFile("m1", 3),
			"data_old":          locDir(),
			"data_old/a.bin":    locFile("m2", 4),
		},
		Remote: map[string]RemoteItem{
			"notes":           remDir("d1", 1),
			"notes/README.md": remFile("f1", "m1", 3, 1),
			"data":            remDir("d2", 1),
			"data/a.bin":      remFile("f2", "m2", 4, 1),
		},
		UnscannedLocal: map[string]string{
			"other/README.md":       "symlink; not followed",
			"App.app/Contents/data": "symlink; not followed",
		},
	})
	plan, _ := Plan(in)
	var moves, uploads int
	for _, a := range plan {
		switch a.Type {
		case MoveRemote:
			moves++
		case Upload:
			uploads++
		}
	}
	if moves != 2 || uploads != 0 {
		t.Fatalf("want the file move and the folder rename as two moves, no upload; got %+v", plan)
	}
}

// Accepted residuals, pinned so MANUAL §8 stays true: the link renamed
// (route 1), and the link and its parent both renamed even with a sibling
// that pairs the parent rename (route 3). The rows sit at no unscanned path
// and read as deleted locally.
func TestARenamedLinkIsADeletion(t *testing.T) {
	in := symlinkedFolderWorld()
	holdSymlink(&in, "pics") // photos (a link) renamed to pics
	if plan, _ := Plan(in); deleteClassCount(plan) == 0 {
		t.Fatalf("route 1 changed (now %+v): update MANUAL §8 and specs §5", plan)
	}

	in = linkInRenamedParentWorld()
	in.Base["a/x.txt"] = baseFile("f3", "m3", 5)
	in.Remote["a/x.txt"] = remFile("f3", "m3", 5, 1)
	in.Local["b/x.txt"] = locFile("m3", 5)
	in.UnscannedLocal = map[string]string{"b/link2": "symlink; not followed"}
	if plan, _ := Plan(in); deleteClassCount(plan) == 0 {
		t.Fatalf("route 3 changed (now %+v): update MANUAL §8 and specs §5", plan)
	}
}
