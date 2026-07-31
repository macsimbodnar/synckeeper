package reconcile

import "testing"

// W18.2 — the property the whole of W18 rests on, asserted rather than
// assumed: an EMPTY baseline structurally cannot produce a delete-class
// action (spec §11). `statedb.ResetBaseline` is only useful because of this,
// and both of W18's "a missing root is never a deletion" rules are built on
// it, so it is pinned here at the level where it is actually true — the pure
// planner, no filesystem, no Drive.
//
// Each case runs twice: once with the baseline intact, which is the
// catastrophe the reset exists to prevent, and once with it cleared, which is
// what must happen instead.

func deleteClassCount(plan []Action) int {
	n := 0
	for _, a := range plan {
		switch a.Type {
		case TrashRemote, QuarantineLocal:
			n++
		}
	}
	return n
}

func countOf(plan []Action, t Type) int {
	n := 0
	for _, a := range plan {
		if a.Type == t {
			n++
		}
	}
	return n
}

// The local sync dir was recreated empty (W18 item D): every tracked file is
// gone locally while Drive still holds it. With the baseline intact this
// empties the user's Drive folder; with it reset, the files come back.
func TestEmptyBaselineTurnsAVanishedLocalTreeIntoDownloads(t *testing.T) {
	base := map[string]BaseItem{
		"a.txt":      baseFile("f1", "m1", 3),
		"docs":       baseDir("f2"),
		"docs/b.txt": baseFile("f3", "m2", 5),
		"docs/c.txt": baseFile("f4", "m3", 7),
	}
	remote := map[string]RemoteItem{
		"a.txt":      remFile("f1", "m1", 3, 1),
		"docs":       remDir("f2", 1),
		"docs/b.txt": remFile("f3", "m2", 5, 1),
		"docs/c.txt": remFile("f4", "m3", 7, 1),
	}
	local := map[string]LocalItem{} // the sync dir was recreated, and is empty

	withBase, _ := Plan(Input{Base: base, Local: local, Remote: remote, Machine: "box", Now: testNow})
	if got := deleteClassCount(withBase); got == 0 {
		t.Fatal("precondition failed: with the baseline intact a vanished local tree must plan deletes — otherwise this test proves nothing")
	} else {
		t.Logf("baseline intact: %d delete-class actions (this is what would empty the Drive folder)", got)
	}

	base = map[string]BaseItem{} // statedb.ResetBaseline
	afterReset, _ := Plan(Input{Base: base, Local: local, Remote: remote, Machine: "box", Now: testNow})
	if got := deleteClassCount(afterReset); got != 0 {
		t.Errorf("empty baseline planned %d delete-class actions; §11 says it structurally cannot: %v", got, afterReset)
	}
	if got := countOf(afterReset, Download); got != 3 {
		t.Errorf("downloads = %d, want 3 (every remote file comes back)", got)
	}
	if got := countOf(afterReset, MkdirLocal); got != 1 {
		t.Errorf("mkdir_local = %d, want 1 (the docs folder is recreated)", got)
	}
}

// The Drive root was repointed at a new, empty folder (W18 item A): every
// tracked file is gone remotely while the disk still holds it. With the
// baseline intact this is F1 — the reproduced bug that moved the user's whole
// tree to the system bin. With it reset, the files upload instead.
func TestEmptyBaselineTurnsAVanishedRemoteTreeIntoUploads(t *testing.T) {
	base := map[string]BaseItem{
		"a.txt":      baseFile("f1", "m1", 3),
		"docs":       baseDir("f2"),
		"docs/b.txt": baseFile("f3", "m2", 5),
		"docs/c.txt": baseFile("f4", "m3", 7),
	}
	local := map[string]LocalItem{
		"a.txt":      locFile("m1", 3),
		"docs":       locDir(),
		"docs/b.txt": locFile("m2", 5),
		"docs/c.txt": locFile("m3", 7),
	}
	remote := map[string]RemoteItem{} // a brand-new, empty Drive folder

	withBase, _ := Plan(Input{Base: base, Local: local, Remote: remote, Machine: "box", Now: testNow})
	if got := deleteClassCount(withBase); got == 0 {
		t.Fatal("precondition failed: with the baseline intact a vanished remote tree must plan deletes — otherwise this test proves nothing")
	} else {
		t.Logf("baseline intact: %d delete-class actions (this is F1, the reproduced tree-to-the-bin bug)", got)
	}

	base = map[string]BaseItem{} // statedb.ResetBaseline
	afterReset, _ := Plan(Input{Base: base, Local: local, Remote: remote, Machine: "box", Now: testNow})
	if got := deleteClassCount(afterReset); got != 0 {
		t.Errorf("empty baseline planned %d delete-class actions; §11 says it structurally cannot: %v", got, afterReset)
	}
	if got := countOf(afterReset, Upload); got != 3 {
		t.Errorf("uploads = %d, want 3 (every local file goes up)", got)
	}
	if got := countOf(afterReset, MkdirRemote); got != 1 {
		t.Errorf("mkdir_remote = %d, want 1 (the docs folder is created on Drive)", got)
	}
}
