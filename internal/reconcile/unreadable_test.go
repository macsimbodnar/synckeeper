package reconcile

import "testing"

// W18.12 — the mandatory half of W18-G. Tolerating an unreadable directory is
// WORSE than wedging on it unless the baseline rows under that directory are
// held harmless: everything inside it is missing from the local snapshot, and
// reconcile reads "missing locally, present on Drive" as a deletion the user
// made. One lost read permission would then trash a whole subtree on Drive.
//
// Each case is planned twice — once without the held set, which is the
// catastrophe, and once with it — so the test cannot quietly stop proving
// anything if the delete path changes.

func unreadableWorld() Input {
	return Input{
		Base: map[string]BaseItem{
			"keep.txt":          baseFile("f1", "m1", 3),
			"locked":            baseDir("d1"),
			"locked/a.txt":      baseFile("f2", "m2", 5),
			"locked/deep":       baseDir("d2"),
			"locked/deep/b.txt": baseFile("f3", "m3", 7),
		},
		// What the scan produced: the locked folder is there, its contents
		// are not, and the rest of the tree scanned normally.
		Local: map[string]LocalItem{
			"keep.txt": locFile("m1", 3),
			"locked":   locDir(),
		},
		Remote: map[string]RemoteItem{
			"keep.txt":          remFile("f1", "m1", 3, 1),
			"locked":            remDir("d1", 1),
			"locked/a.txt":      remFile("f2", "m2", 5, 1),
			"locked/deep":       remDir("d2", 1),
			"locked/deep/b.txt": remFile("f3", "m3", 7, 1),
		},
	}
}

func TestUnreadableSubtreeIsNeverReadAsALocalDeletion(t *testing.T) {
	in := withTestIdentity(unreadableWorld())

	// Without the held set — what tolerating the unreadable directory would
	// mean on its own.
	naive, _ := Plan(in)
	if got := deleteClassCount(naive); got == 0 {
		t.Fatal("precondition failed: an unseen subtree must otherwise plan deletes — without that this test proves nothing")
	} else {
		t.Logf("unheld: %d delete-class actions (this is what would empty the subtree on Drive)", got)
	}

	// With it: nothing is deleted, nothing is transferred, and the rows are
	// reported rather than silently ignored.
	in.UnreadableLocal = map[string]bool{"f2": true, "f3": true, "d2": true}
	held, skips := Plan(in)
	if got := deleteClassCount(held); got != 0 {
		t.Fatalf("delete-class actions = %d, want 0: %+v", got, held)
	}
	if len(held) != 0 {
		t.Fatalf("want an empty plan for an unreadable subtree, got %+v", held)
	}
	reported := map[string]bool{}
	for _, s := range skips {
		reported[s.RelPath] = true
	}
	for _, want := range []string{"locked/a.txt", "locked/deep", "locked/deep/b.txt"} {
		if !reported[want] {
			t.Errorf("%s was held harmless but never reported; a silent hold is indistinguishable from a bug", want)
		}
	}
}

// The rest of the tree keeps syncing — the fix is worthless if holding the
// subtree harmless also freezes everything else.
func TestTheRestOfTheTreeStillSyncsAroundAnUnreadableSubtree(t *testing.T) {
	in := withTestIdentity(unreadableWorld())
	in.UnreadableLocal = map[string]bool{"f2": true, "f3": true, "d2": true}
	in.Local["new.txt"] = locFile("m9", 9)              // created while the folder was locked
	in.Remote["theirs.txt"] = remFile("r9", "m8", 8, 1) // and one arrived from Drive

	plan, _ := Plan(in)
	var uploads, downloads int
	for _, a := range plan {
		switch a.Type {
		case Upload:
			uploads++
		case Download:
			downloads++
		default:
			t.Errorf("unexpected action beside the unreadable subtree: %+v", a)
		}
	}
	if uploads != 1 || downloads != 1 {
		t.Fatalf("uploads/downloads = %d/%d, want 1/1: %+v", uploads, downloads, plan)
	}
}

// A file deleted for real, outside the unreadable subtree, still propagates.
// The hold must be scoped to what the scan could not see, not a blanket
// "something went wrong, do nothing".
func TestAGenuineDeletionOutsideTheSubtreeStillPropagates(t *testing.T) {
	in := withTestIdentity(unreadableWorld())
	in.UnreadableLocal = map[string]bool{"f2": true, "f3": true, "d2": true}
	delete(in.Local, "keep.txt") // the user deleted this one, and the scan saw that

	plan, _ := Plan(in)
	if len(plan) != 1 || plan[0].Type != TrashRemote || plan[0].RelPath != "keep.txt" {
		t.Fatalf("want the readable file's deletion to propagate, got %+v", plan)
	}
}
