package reconcile

import "testing"

// S014 (same held-harmless family): rows under an unreadable directory are
// absent from Local because the scan could not look, not because they moved.
// A copy of the locked content elsewhere must not pair as the locked
// subfolder's rename — that would move the Drive folder out from under the
// folder the user merely locked.
func TestACopyOfAnUnreadableSubtreeIsNotItsRename(t *testing.T) {
	in := withTestIdentity(unreadableWorld())
	in.UnreadableLocal = map[string]bool{"f2": true, "f3": true, "d2": true}
	in.Local["copy"] = locDir()
	in.Local["copy/deep"] = locDir()
	in.Local["copy/deep/b.txt"] = locFile("m3", 7)

	plan, _ := Plan(in)
	for _, a := range plan {
		if a.Type == MoveRemote || a.FileID == "d2" || a.FileID == "f3" {
			t.Fatalf("the locked subtree was moved by a copy of its content: %+v", plan)
		}
	}
	var uploads int
	for _, a := range plan {
		if a.Type == Upload {
			uploads++
		}
	}
	if uploads != 1 {
		t.Fatalf("want the copy uploaded as a new file, got %+v", plan)
	}
}
