package reconcile

import "testing"

// W18.9 — a download onto a path the same plan empties must expect it EMPTY.
//
// The §7 overwrite guard means "this exact file must still be here, or refuse
// and replan". Pass 3 pinned the occupant the scan saw at the download's path
// without checking whether an earlier stage had already been told to move it
// away, so the guard refused the download every cycle: a permanent wedge, and
// self-inflicted rather than a real mid-cycle edit.
//
// Found building W18-E — whose conflict shape produces this pair on the other
// machine every time — but it predates it, and this is the minimized form with
// no init merge in sight: one machine renames a file and gives a new file the
// old name; the second machine plans a local move off that path plus a
// download onto it.
func TestDownloadOntoAVacatedPathExpectsItEmpty(t *testing.T) {
	in := Input{
		Base:  map[string]BaseItem{"notes.txt": baseFile("r1", "m1", 3)},
		Local: map[string]LocalItem{"notes.txt": locFile("m1", 3)},
		Remote: map[string]RemoteItem{
			"other.txt": remFile("r1", "m1", 3, 2), // the tracked file, renamed on Drive
			"notes.txt": remFile("r2", "m2", 5, 1), // a NEW file under the freed name
		},
	}
	runPlan(t, withTestIdentity(in), []step{
		{t: MoveLocal, rel: "notes.txt", newRel: "other.txt", fileID: "r1"},
		{t: Download, rel: "notes.txt", fileID: "r2"},
		{t: Record, rel: "other.txt", fileID: "r1"}, // the moved file's row follows it
	})

	plan, _ := Plan(withTestIdentity(in))
	for _, a := range plan {
		if a.Type != Download {
			continue
		}
		if a.LocalExists {
			t.Fatalf("download of %s expects an occupant (size %d, mtime %d), but the move stage empties that path first — the §7 guard would refuse it every cycle",
				a.RelPath, a.LocalSize, a.LocalMtimeNS)
		}
	}
}

// The pin must survive where it is right: an ordinary replace-in-place, where
// nothing moves the occupant away and the guard is the only thing standing
// between a mid-cycle edit and being clobbered.
func TestDownloadReplacingAnUntouchedFileStillPinsIt(t *testing.T) {
	in := Input{
		Base:   map[string]BaseItem{},
		Local:  map[string]LocalItem{"notes.txt": locFileM("m1", 3, 4242)},
		Remote: map[string]RemoteItem{"notes.txt": remFile("r2", "m1", 3, 1)},
	}
	// Same md5 on both sides: an adopt, which pins the stat for the same
	// reason. The download form needs a baseline row to differ, so use the
	// untracked-remote-replaces-untracked-local path instead.
	plan, _ := Plan(withTestIdentity(in))
	if len(plan) != 1 || plan[0].Type != Record {
		t.Fatalf("want a single adopt, got %+v", plan)
	}
	if !plan[0].LocalExists || plan[0].LocalMtimeNS != 4242 {
		t.Fatalf("adopt must pin the scanned file: %+v", plan[0])
	}

	// And the genuine replace-in-place: a tracked file the remote changed.
	in2 := Input{
		Base:   map[string]BaseItem{"notes.txt": baseFile("r1", "m1", 3)},
		Local:  map[string]LocalItem{"notes.txt": locFileM("m1", 3, 4242)},
		Remote: map[string]RemoteItem{"notes.txt": remFile("r1", "m2", 5, 2)},
	}
	plan2, _ := Plan(withTestIdentity(in2))
	if len(plan2) != 1 || plan2[0].Type != Download {
		t.Fatalf("want a single download, got %+v", plan2)
	}
	if !plan2[0].LocalExists || plan2[0].LocalMtimeNS != 4242 {
		t.Fatalf("download replacing an untouched file must pin it: %+v", plan2[0])
	}
}
