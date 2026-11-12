package engine

// S002, route 1 — no crash needed: both machines rename folder D to N, then b
// edits N/f.txt and syncs. Was: every cycle on a refused its plan with
// `plan violates §4.5: download and upload both act on "N/f.txt"`, so a never
// synced again until something external changed.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/executor"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// s002BothRenameFolder leaves a and b with D renamed to N on both disks, b's
// rename synced (one reparent on Drive), and returns the folder's and the
// file's Drive ids from before the renames.
func s002BothRenameFolder(t *testing.T) (fake *driveclient.Fake, a, b *machine, root, folderID, fileID string) {
	t.Helper()
	fake, root = newWorld(t)
	a = newMachine(t, "a", fake, root)
	b = newMachine(t, "b", fake, root)
	a.write(t, "D/f.txt", "v1, synced everywhere")
	a.sync(t)
	b.sync(t)
	folderID = remoteChildID(t, fake, root, "D")
	fileID = remoteChildID(t, fake, folderID, "f.txt")

	a.rename(t, "D", "N")
	b.rename(t, "D", "N")
	b.sync(t)
	return fake, a, b, root, folderID, fileID
}

// s002WantFolderKept asserts Drive holds exactly the one folder N, the item
// the renames started from — no delete-and-recreate of the folder.
func s002WantFolderKept(t *testing.T, fake *driveclient.Fake, root, folderID string) {
	t.Helper()
	kids, err := fake.List(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 1 || kids[0].Name != "N" || kids[0].ID != folderID || !kids[0].IsDir() {
		var got []string
		for _, k := range kids {
			got = append(got, k.Name+"="+k.ID)
		}
		t.Errorf("[drive] root holds %v, want only folder N = %s (the renamed folder)", got, folderID)
	}
}

func TestS002BothRenameFolderOneEditsSyncs(t *testing.T) {
	fake, a, b, root, folderID, fileID := s002BothRenameFolder(t)
	const v2 = "v2, edited on b after the rename"
	b.write(t, "N/f.txt", v2)
	b.sync(t)

	for i := 0; i < 3; i++ {
		a.sync(t) // a refused plan fails the test here
		b.sync(t)
	}
	wantOnly(t, "a", a.listTree(t), "N/f.txt", v2)
	wantOnly(t, "b", b.listTree(t), "N/f.txt", v2)
	wantOnly(t, "drive", driveContents(t, fake, folderID), "f.txt", v2)
	s002WantFolderKept(t, fake, root, folderID)
	wantSameItem(t, fake, folderID, "f.txt", fileID)
}

func TestS002BothRenameFolderBothEditConflicts(t *testing.T) {
	fake, a, b, root, folderID, fileID := s002BothRenameFolder(t)
	const editA, editB = "edit made on a", "edit made on b, longer"
	b.write(t, "N/f.txt", editB)
	b.sync(t)
	a.write(t, "N/f.txt", editA)

	for i := 0; i < 3; i++ {
		a.sync(t)
		b.sync(t)
	}
	for where, tree := range map[string]map[string]string{
		"a": a.listTree(t), "b": b.listTree(t), "drive": prefixed("N/", driveContents(t, fake, folderID)),
	} {
		if len(tree) != 2 || tree["N/f.txt"] != editB {
			t.Errorf("[%s] want N/f.txt = %q and one conflict copy, have %v", where, editB, tree)
			continue
		}
		for name, content := range tree {
			if name != "N/f.txt" && (!strings.HasPrefix(name, "N/f (conflict a ") || content != editA) {
				t.Errorf("[%s] want a's edit under a's conflict name, have %s = %q", where, name, content)
			}
		}
	}
	s002WantFolderKept(t, fake, root, folderID)
	wantSameItem(t, fake, folderID, "f.txt", fileID)
}

func prefixed(prefix string, tree map[string]string) map[string]string {
	out := make(map[string]string, len(tree))
	for k, v := range tree {
		out[prefix+k] = v
	}
	return out
}

// Cause D, no crash: b.txt moved over a.txt on both machines — b syncs the
// deletion of a.txt and then the rename; a made the same two changes and
// syncs after. Was: a refused its plan every cycle (`upload and download both
// act on "a.txt"`).
func TestS002FileMovedOverAnotherOnBothSyncs(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "a.txt", "content of a")
	a.write(t, "b.txt", "content of b, longer")
	a.sync(t)
	b.sync(t)
	id := driveFileNamed(t, fake, root, "b.txt")

	b.remove(t, "a.txt")
	b.sync(t)
	b.rename(t, "b.txt", "a.txt")
	b.sync(t)
	a.rename(t, "b.txt", "a.txt") // over a.txt

	for i := 0; i < 3; i++ {
		a.sync(t)
		b.sync(t)
	}
	const want = "content of b, longer"
	wantOnly(t, "a", a.listTree(t), "a.txt", want)
	wantOnly(t, "b", b.listTree(t), "a.txt", want)
	wantOnly(t, "drive", driveContents(t, fake, root), "a.txt", want)
	wantSameItem(t, fake, root, "a.txt", id)
}

// S1 (round-2 review), engine: a deletes D and creates an unrelated N/f.txt;
// b renames D to N and syncs. Only the folder's name ties a's N to D, so a's
// file must not be uploaded over Drive's f.txt: both kept, Drive's at the name,
// a's as a conflict copy, on a, b and Drive; the folder and f.txt keep their ids.
func TestS002UnrelatedFolderAtTheRenamedNameKeepsBoth(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	const orig, other = "D's f, synced everywhere", "an unrelated file made on a"
	a.write(t, "D/f.txt", orig)
	a.sync(t)
	b.sync(t)
	folderID := remoteChildID(t, fake, root, "D")
	fileID := remoteChildID(t, fake, folderID, "f.txt")

	a.remove(t, "D")
	a.write(t, "N/f.txt", other)
	b.rename(t, "D", "N")
	b.sync(t)

	for i := 0; i < 3; i++ {
		a.sync(t)
		b.sync(t)
	}
	for where, tree := range map[string]map[string]string{
		"a": a.listTree(t), "b": b.listTree(t), "drive": prefixed("N/", driveContents(t, fake, folderID)),
	} {
		if len(tree) != 2 || tree["N/f.txt"] != orig {
			t.Errorf("[%s] want N/f.txt = %q and one conflict copy, have %v", where, orig, tree)
			continue
		}
		for name, content := range tree {
			if name != "N/f.txt" && (!strings.HasPrefix(name, "N/f (conflict a ") || content != other) {
				t.Errorf("[%s] want a's file under a's conflict name, have %s = %q", where, name, content)
			}
		}
	}
	s002WantFolderKept(t, fake, root, folderID)
	wantSameItem(t, fake, folderID, "f.txt", fileID)
}

// W1 (round-2 review): Drive renamed D to N and swapped g.txt with N/x.txt;
// here only D was renamed to N. Both occupants are tracked files whose bytes
// are on Drive — the swap's own rule (clobber, pinned) applies; no conflict
// copy may appear (round 1 backed up N/x.txt as untracked and uploaded a
// duplicate of x's bytes).
func TestS002SwapUnderARenamedFolderMakesNoCopy(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	const g, x = "g's bytes", "x's bytes, longer"
	a.write(t, "g.txt", g)
	a.write(t, "D/x.txt", x)
	a.sync(t)
	b.sync(t)

	b.rename(t, "D", "N")
	b.sync(t)
	// The swap by id, as the Drive web UI does it (a local swap on b would
	// sync as two in-place edits, not a swap).
	ctx := context.Background()
	folderID := remoteChildID(t, fake, root, "N")
	gID, xID := remoteChildID(t, fake, root, "g.txt"), remoteChildID(t, fake, folderID, "x.txt")
	for _, mv := range []struct{ id, parent, name string }{
		{gID, root, "tmp.txt"}, {xID, root, "g.txt"}, {gID, folderID, "x.txt"},
	} {
		if _, err := fake.Move(ctx, mv.id, mv.parent, mv.name); err != nil {
			t.Fatal(err)
		}
	}
	a.rename(t, "D", "N")

	// A swap's two moves fail their commits for a cycle or two and then
	// converge — the same without the folder rename (pre-existing, not S002).
	// What this test pins: no plan refused, no conflict copy, the end state.
	for i := 0; i < 4; i++ {
		for _, m := range []*machine{a, b} {
			if _, err := m.eng.Sync(ctx, Options{}); err != nil {
				t.Fatalf("[%s] cycle %d: %v", m.name, i, err)
			}
		}
	}
	a.sync(t) // settled: a clean cycle on both
	b.sync(t)
	want := map[string]string{"g.txt": x, "N/x.txt": g}
	for where, tree := range map[string]map[string]string{"a": a.listTree(t), "b": b.listTree(t)} {
		if len(tree) != 2 || tree["g.txt"] != x || tree["N/x.txt"] != g {
			t.Errorf("[%s] have %v, want %v", where, tree, want)
		}
	}
	if top := driveContents(t, fake, root); len(top) != 1 || top["g.txt"] != x {
		t.Errorf("[drive] root files %v, want only g.txt = %q", top, x)
	}
	if in := driveContents(t, fake, remoteChildID(t, fake, root, "N")); len(in) != 1 || in["x.txt"] != g {
		t.Errorf("[drive] N holds %v, want only x.txt = %q", in, g)
	}
}

// W2 (round-2 review): Drive renamed D to N, deleted N/x.txt, then moved g.txt
// onto N/x.txt; here only D was renamed. x is unchanged here and deleted on
// Drive (§4.2: to the system bin), g moved on Drive (§4.2: moved here) — the
// bin must come first, then the move. Was: a conflict copy of x, the move,
// and a quarantine of N/x.txt pinned to x's stat that then found g's file
// (a failed action every time; with matching stats, g's file binned).
func TestS002MoveOntoADeletedFileUnderARenamedFolder(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	const g, x = "g's bytes", "x's bytes, longer"
	a.write(t, "g.txt", g)
	a.write(t, "D/x.txt", x)
	a.sync(t)
	b.sync(t)

	b.rename(t, "D", "N")
	b.sync(t)
	b.remove(t, "N/x.txt")
	b.sync(t)
	b.rename(t, "g.txt", "N/x.txt")
	b.sync(t)
	a.rename(t, "D", "N")

	for i := 0; i < 3; i++ {
		a.sync(t) // a failed action fails the test here
		b.sync(t)
	}
	wantOnly(t, "a", a.listTree(t), "N/x.txt", g)
	wantOnly(t, "b", b.listTree(t), "N/x.txt", g)
	if top := driveContents(t, fake, root); len(top) != 0 {
		t.Errorf("[drive] root files %v, want none", top)
	}
	wantOnly(t, "drive", driveContents(t, fake, remoteChildID(t, fake, root, "N")), "x.txt", g)
	if got, err := os.ReadFile(a.binPath("x.txt")); err != nil || string(got) != x {
		t.Errorf("[a] bin x.txt = %q, %v; want x's bytes, binned", got, err)
	}
}

// --- Round 3 ---------------------------------------------------------------

// Round 3, item 2: both renamed D to N and f.txt to g.txt; a then made a new
// N/f.txt. g.txt is the synced file (its bytes say so); f.txt is new.
func TestS002BothRenamedFolderAndFileThenNewFileAtTheOldName(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	const x, q = "f's bytes, synced", "a new file made on a"
	a.write(t, "D/f.txt", x)
	a.sync(t)
	b.sync(t)
	folderID := remoteChildID(t, fake, root, "D")
	fileID := remoteChildID(t, fake, folderID, "f.txt")

	b.rename(t, "D", "N")
	b.sync(t)
	b.rename(t, "N/f.txt", "N/g.txt")
	b.sync(t)
	a.rename(t, "D", "N")
	a.rename(t, "N/f.txt", "N/g.txt")
	a.write(t, "N/f.txt", q)

	for i := 0; i < 3; i++ {
		a.sync(t)
		b.sync(t)
	}
	want := map[string]string{"N/f.txt": q, "N/g.txt": x}
	for where, tree := range map[string]map[string]string{
		"a": a.listTree(t), "b": b.listTree(t), "drive": prefixed("N/", driveContents(t, fake, folderID)),
	} {
		if len(tree) != 2 || tree["N/f.txt"] != q || tree["N/g.txt"] != x {
			t.Errorf("[%s] have %v, want %v", where, tree, want)
		}
	}
	s002WantFolderKept(t, fake, root, folderID)
	wantSameItem(t, fake, folderID, "g.txt", fileID)
}

// Round 3, item 3: S1's unrelated pair, interrupted at each point the
// injected checkpoints reach (the copy's upload — after the backup and the
// folder's commit — and the download, before and after its temp file). No
// cycle may plan a trash or quarantine of Drive's f.txt, and all converge to
// Drive's bytes at the name and a's at the conflict name, everywhere.
func TestS002UnrelatedPairSurvivesEveryInterruption(t *testing.T) {
	for _, cp := range []string{executor.CPUploadBeforeCommit, executor.CPDownloadTempWritten, executor.CPDownloadBeforeCommit} {
		t.Run(cp, func(t *testing.T) {
			fake, root := newWorld(t)
			a := newMachine(t, "a", fake, root)
			b := newMachine(t, "b", fake, root)
			const orig, other = "D's f, synced everywhere", "an unrelated file made on a"
			a.write(t, "D/f.txt", orig)
			a.sync(t)
			b.sync(t)
			folderID := remoteChildID(t, fake, root, "D")
			fileID := remoteChildID(t, fake, folderID, "f.txt")
			a.remove(t, "D")
			a.write(t, "N/f.txt", other)
			b.rename(t, "D", "N")
			b.sync(t)

			fired := false
			executor.FaultHook = func(name string) error {
				if name == cp && !fired {
					fired = true
					return errors.New("injected crash at " + name)
				}
				return nil
			}
			t.Cleanup(func() { executor.FaultHook = nil })
			for i := 0; i < 5; i++ {
				res, err := a.eng.Sync(context.Background(), Options{})
				if err != nil {
					t.Fatalf("[a] cycle %d: %v", i, err)
				}
				for _, act := range res.Plan {
					if (act.Type == reconcile.TrashRemote || act.Type == reconcile.QuarantineLocal) &&
						(act.FileID == fileID || strings.HasPrefix(act.RelPath, "N/f")) {
						t.Errorf("[a] cycle %d plans %s %q (%s)", i, act.Type, act.RelPath, act.FileID)
					}
				}
			}
			if !fired {
				t.Fatalf("checkpoint %s never reached", cp)
			}
			executor.FaultHook = nil
			for i := 0; i < 2; i++ {
				a.sync(t)
				b.sync(t)
			}
			for where, tree := range map[string]map[string]string{
				"a": a.listTree(t), "b": b.listTree(t), "drive": prefixed("N/", driveContents(t, fake, folderID)),
			} {
				if len(tree) != 2 || tree["N/f.txt"] != orig {
					t.Errorf("[%s] want N/f.txt = %q and one conflict copy, have %v", where, orig, tree)
					continue
				}
				for name, content := range tree {
					if name != "N/f.txt" && (!strings.HasPrefix(name, "N/f (conflict a ") || content != other) {
						t.Errorf("[%s] want a's file under a's conflict name, have %s = %q", where, name, content)
					}
				}
			}
			wantSameItem(t, fake, folderID, "f.txt", fileID)
		})
	}
}

// Round 3, item 4a: Drive deleted x's item and moved g onto x; x was edited
// here. One cycle, no failed action: x's edit kept as a conflict copy, g at x.
func TestS002MoveOntoADeletedEditedFileOneCycle(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	const g, x, edit = "g's bytes", "x's bytes, longer", "x edited on a"
	a.write(t, "g.txt", g)
	a.write(t, "x.txt", x)
	a.sync(t)
	b.sync(t)
	b.remove(t, "x.txt")
	b.sync(t)
	b.rename(t, "g.txt", "x.txt")
	b.sync(t)
	a.write(t, "x.txt", edit)

	a.sync(t) // any failed action fails the test here
	b.sync(t)
	for where, tree := range map[string]map[string]string{
		"a": a.listTree(t), "b": b.listTree(t), "drive": driveContents(t, fake, root),
	} {
		if len(tree) != 2 || tree["x.txt"] != g {
			t.Errorf("[%s] want x.txt = %q and a's edit as a conflict copy, have %v", where, g, tree)
			continue
		}
		for name, content := range tree {
			if name != "x.txt" && (!strings.HasPrefix(name, "x (conflict a ") || content != edit) {
				t.Errorf("[%s] have %s = %q", where, name, content)
			}
		}
	}
}

// Round 3, item 4b: Drive renamed P to Q, deleted Q/x.txt's item and moved
// g.txt onto it; here nothing changed. The early bin acts where the folder's
// hoisted move has put x (Q/x.txt): one cycle, no failed action.
func TestS002MoveOntoADeletedFileUnderAPendingFolderMove(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	const g, x = "g's bytes", "x's bytes, longer"
	a.write(t, "g.txt", g)
	a.write(t, "P/x.txt", x)
	a.sync(t)
	b.sync(t)
	b.rename(t, "P", "Q")
	b.sync(t)
	b.remove(t, "Q/x.txt")
	b.sync(t)
	b.rename(t, "g.txt", "Q/x.txt")
	b.sync(t)

	a.sync(t) // any failed action fails the test here
	wantOnly(t, "a", a.listTree(t), "Q/x.txt", g)
	if got, err := os.ReadFile(a.binPath("x.txt")); err != nil || string(got) != x {
		t.Errorf("[a] bin x.txt = %q, %v; want x's bytes, binned", got, err)
	}
}
