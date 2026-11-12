package engine

// S031, engine level: two machines, the real executor, the in-memory Drive
// fake. The other machine's renames are made on Drive by id (as the Drive web
// UI or another client does): from a disk, a rename onto an occupied name
// syncs as an edit of the occupant, not as a move. Each test asserts the end
// state on both machines and Drive after settling — the edit where §4.2 puts
// it, every item holding its own bytes — and every cycle runs without a failed
// action or a refused plan unless the test says why.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/executor"
)

type driveMove struct{ id, parent, name string }

func moveOnDrive(t *testing.T, fake *driveclient.Fake, mvs ...driveMove) {
	t.Helper()
	for _, mv := range mvs {
		if _, err := fake.Move(context.Background(), mv.id, mv.parent, mv.name); err != nil {
			t.Fatal(err)
		}
	}
}

// settleAll runs clean cycles on every machine until a full round plans nothing;
// m.sync fails the test on a refused plan or a failed action.
func settleAll(t *testing.T, ms ...*machine) {
	t.Helper()
	for round := 0; round < 6; round++ {
		busy := 0
		for _, m := range ms {
			busy += len(m.sync(t).Plan)
		}
		if busy == 0 {
			return
		}
	}
	t.Fatal("did not settle within 6 rounds")
}

// wantTrees: both machines and Drive hold exactly want, except that a
// conflict copy with the content in copyOf (if non-empty) must exist too.
func wantTrees(t *testing.T, fake *driveclient.Fake, root string, want map[string]string, copyOf string, ms ...*machine) {
	t.Helper()
	trees := map[string]map[string]string{"drive": driveTreeOf(t, fake, root)}
	for _, m := range ms {
		trees[m.name] = m.listTree(t)
	}
	for where, tree := range trees {
		extra := 0
		for rel, content := range tree {
			if w, ok := want[rel]; ok {
				if content != w {
					t.Errorf("[%s] %s = %q, want %q", where, rel, content, w)
				}
				continue
			}
			if copyOf != "" && strings.Contains(rel, " (conflict ") && content == copyOf {
				extra++
				continue
			}
			t.Errorf("[%s] unexpected %s = %q", where, rel, content)
		}
		for rel := range want {
			if _, ok := tree[rel]; !ok {
				t.Errorf("[%s] %s missing", where, rel)
			}
		}
		if copyOf != "" && extra != 1 {
			t.Errorf("[%s] want one conflict copy holding %q, have %d: %v", where, copyOf, extra, tree)
		}
	}
}

// F4 flat: Drive renamed f.txt → h.txt and moved a.txt onto f.txt; a edited
// f.txt and had not synced it. Only a changed f's content: the edit follows
// its file to h.txt and goes up into F; a's bytes stay G's.
func TestS031F4FlatTwoMachines(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	const fx, ag, edit = "f, synced everywhere", "a's bytes, longer", "f edited on a, not yet synced"
	a.write(t, "f.txt", fx)
	a.write(t, "a.txt", ag)
	a.sync(t)
	b.sync(t)
	fID, gID := driveFileNamed(t, fake, root, "f.txt"), driveFileNamed(t, fake, root, "a.txt")
	moveOnDrive(t, fake, driveMove{fID, root, "h.txt"}, driveMove{gID, root, "f.txt"})
	a.write(t, "f.txt", edit)

	settle(t, a, b)
	wantTrees(t, fake, root, map[string]string{"h.txt": edit, "f.txt": ag}, "", a, b)
	wantSameItem(t, fake, root, "h.txt", fID)
	wantSameItem(t, fake, root, "f.txt", gID)
}

// F4 one folder deeper: g.txt sorted before S/f.txt by depth.
func TestS031F4DeeperTwoMachines(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	const fx, gg, edit = "f, synced everywhere", "g's bytes, longer", "f edited on a, not yet synced"
	a.write(t, "S/f.txt", fx)
	a.write(t, "g.txt", gg)
	a.sync(t)
	b.sync(t)
	sID := remoteChildID(t, fake, root, "S")
	fID, gID := driveFileNamed(t, fake, sID, "f.txt"), driveFileNamed(t, fake, root, "g.txt")
	moveOnDrive(t, fake, driveMove{fID, sID, "h.txt"}, driveMove{gID, sID, "f.txt"})
	a.write(t, "S/f.txt", edit)

	settle(t, a, b)
	wantTrees(t, fake, root, map[string]string{"S/h.txt": edit, "S/f.txt": gg}, "", a, b)
	wantSameItem(t, fake, sID, "h.txt", fID)
	wantSameItem(t, fake, sID, "f.txt", gID)
}

// s031Chain: a.txt, b.txt, c.txt synced; Drive moves c→d, b→c, a→b by id; a
// edits b.txt. Returns the ids of a, b and c.
func s031Chain(t *testing.T) (fake *driveclient.Fake, root string, a, b *machine, ids [3]string) {
	t.Helper()
	fake, root = newWorld(t)
	a = newMachine(t, "a", fake, root)
	b = newMachine(t, "b", fake, root)
	a.write(t, "a.txt", "content of a")
	a.write(t, "b.txt", "content of b, longer")
	a.write(t, "c.txt", "content of c, longer still")
	a.sync(t)
	b.sync(t)
	for i, n := range []string{"a.txt", "b.txt", "c.txt"} {
		ids[i] = driveFileNamed(t, fake, root, n)
	}
	moveOnDrive(t, fake, driveMove{ids[2], root, "d.txt"}, driveMove{ids[1], root, "c.txt"}, driveMove{ids[0], root, "b.txt"})
	a.write(t, "b.txt", "b edited on a, not yet synced")
	return fake, root, a, b, ids
}

func TestS031ChainOfThreeTwoMachines(t *testing.T) {
	fake, root, a, b, ids := s031Chain(t)
	settle(t, a, b)
	wantTrees(t, fake, root, map[string]string{
		"b.txt": "content of a", "c.txt": "b edited on a, not yet synced", "d.txt": "content of c, longer still",
	}, "", a, b)
	wantSameItem(t, fake, root, "b.txt", ids[0])
	wantSameItem(t, fake, root, "c.txt", ids[1])
	wantSameItem(t, fake, root, "d.txt", ids[2])
}

// s031Swap: a.txt and b.txt synced, swapped by id on Drive (through a
// temporary name, as a person does it).
func s031Swap(t *testing.T) (fake *driveclient.Fake, root string, a, b *machine, aID, bID string) {
	t.Helper()
	fake, root = newWorld(t)
	a = newMachine(t, "a", fake, root)
	b = newMachine(t, "b", fake, root)
	a.write(t, "a.txt", "content-A")
	a.write(t, "b.txt", "content-B, longer")
	a.sync(t)
	b.sync(t)
	aID, bID = driveFileNamed(t, fake, root, "a.txt"), driveFileNamed(t, fake, root, "b.txt")
	moveOnDrive(t, fake, driveMove{aID, root, "swap-tmp"}, driveMove{bID, root, "a.txt"}, driveMove{aID, root, "b.txt"})
	return fake, root, a, b, aID, bID
}

// Nothing edited: one cycle, zero failed actions (m.sync), and the next cycle
// plans nothing. Was: both moves failed their commits (`UNIQUE constraint
// failed: items.rel_path`) for a cycle or two.
func TestS031SwapByIDOneCycleNoFailure(t *testing.T) {
	fake, root, a, b, aID, bID := s031Swap(t)
	for _, m := range []*machine{a, b} {
		m.sync(t)
		if res := m.sync(t); len(res.Plan) != 0 {
			t.Errorf("[%s] the cycle after the swap plans %v; want nothing", m.name, res.Plan)
		}
	}
	wantTrees(t, fake, root, map[string]string{"a.txt": "content-B, longer", "b.txt": "content-A"}, "", a, b)
	wantSameItem(t, fake, root, "a.txt", bID)
	wantSameItem(t, fake, root, "b.txt", aID)
}

func TestS031SwapWithOneSideEditedKeepsTheEdit(t *testing.T) {
	fake, root, a, b, aID, bID := s031Swap(t)
	const edit = "a edited on a, not yet synced"
	a.write(t, "a.txt", edit)
	settle(t, a, b)
	wantTrees(t, fake, root, map[string]string{"a.txt": "content-B, longer", "b.txt": edit}, "", a, b)
	wantSameItem(t, fake, root, "a.txt", bID)
	wantSameItem(t, fake, root, "b.txt", aID)
}

// A crash at each move's commit — after the rename on disk, before the row —
// of a swap with one side edited, and of the chain: the replan converges and
// no file is lost. The one crash that leaves a file under the temporary name
// (the first step, before its commit) keeps it there as a conflict copy: the
// edit survives under that name, Drive's item keeps its bytes.
func TestS031SwapAndChainSurviveEveryInterruption(t *testing.T) {
	const edit = "a edited on a, not yet synced"
	for k := 1; k <= 3; k++ {
		t.Run("swap/move "+string(rune('0'+k)), func(t *testing.T) {
			fake, root, a, b, _, _ := s031Swap(t)
			a.write(t, "a.txt", edit)
			crashAtNth(t, a, executor.CPMoveBeforeCommit, k)
			settle(t, a, b)
			if k == 1 {
				wantTrees(t, fake, root, map[string]string{"a.txt": "content-B, longer", "b.txt": "content-A"}, edit, a, b)
				return
			}
			wantTrees(t, fake, root, map[string]string{"a.txt": "content-B, longer", "b.txt": edit}, "", a, b)
		})
		t.Run("chain/move "+string(rune('0'+k)), func(t *testing.T) {
			fake, root, a, b, _ := s031Chain(t)
			crashAtNth(t, a, executor.CPMoveBeforeCommit, k)
			settle(t, a, b)
			wantTrees(t, fake, root, map[string]string{
				"b.txt": "content of a", "c.txt": "b edited on a, not yet synced", "d.txt": "content of c, longer still",
			}, "", a, b)
		})
	}
}

// crashAtNth runs one cycle on m that fails at the n-th time it reaches cp
// (a soft failure, as a crash there would leave it), and checks it got there.
func crashAtNth(t *testing.T, m *machine, cp string, n int) {
	t.Helper()
	seen := 0
	executor.FaultHook = func(name string) error {
		if name == cp {
			seen++
			if seen == n {
				return errors.New("injected crash at " + name)
			}
		}
		return nil
	}
	defer func() { executor.FaultHook = nil }()
	if _, err := m.eng.Sync(context.Background(), Options{}); err != nil {
		t.Fatalf("[%s] crashed cycle: %v", m.name, err)
	}
	if seen < n {
		t.Fatalf("checkpoint %s reached %d times, want %d", cp, seen, n)
	}
}

// s031F1: D/f.txt and g.txt synced; b renames D → N (one reparent); Drive
// then renames N/f.txt → N/h.txt and moves g.txt onto N/f.txt, by id. Returns
// the ids of N, f and g.
func s031F1(t *testing.T) (fake *driveclient.Fake, root string, a, b *machine, nID, fID, gID string) {
	t.Helper()
	fake, root = newWorld(t)
	a = newMachine(t, "a", fake, root)
	b = newMachine(t, "b", fake, root)
	a.write(t, "D/f.txt", "f, synced everywhere")
	a.write(t, "g.txt", "g's bytes, longer")
	a.sync(t)
	b.sync(t)
	b.rename(t, "D", "N")
	b.sync(t)
	nID = remoteChildID(t, fake, root, "N")
	fID, gID = driveFileNamed(t, fake, nID, "f.txt"), driveFileNamed(t, fake, root, "g.txt")
	moveOnDrive(t, fake, driveMove{fID, nID, "h.txt"}, driveMove{gID, nID, "f.txt"})
	return fake, root, a, b, nID, fID, gID
}

// F1: a renamed D → N too and edited N/f.txt. With no other file of D under N
// carrying its baseline bytes, N/f.txt is a stranger by S002's evidence rule:
// kept as a conflict copy; F keeps its bytes at N/h.txt; G's at N/f.txt.
func TestS031F1TwoMachines(t *testing.T) {
	fake, root, a, b, nID, fID, gID := s031F1(t)
	const edit = "N/f.txt edited on a, not yet synced"
	a.rename(t, "D", "N")
	a.write(t, "N/f.txt", edit)

	settle(t, a, b)
	wantTrees(t, fake, root, map[string]string{"N/h.txt": "f, synced everywhere", "N/f.txt": "g's bytes, longer"}, edit, a, b)
	wantSameItem(t, fake, nID, "h.txt", fID)
	wantSameItem(t, fake, nID, "f.txt", gID)
}

// F2: a also renamed N/f.txt → N/h.txt (as Drive did) and made a new N/f.txt.
// N/h.txt is F's file, recorded there; the new N/f.txt is kept as a conflict
// copy before g's file takes its name. Was: refused every cycle.
func TestS031F2TwoMachines(t *testing.T) {
	fake, root, a, b, nID, fID, gID := s031F1(t)
	const fresh = "a new N/f.txt made on a"
	a.rename(t, "D", "N")
	a.rename(t, "N/f.txt", "N/h.txt")
	a.write(t, "N/f.txt", fresh)

	settle(t, a, b)
	wantTrees(t, fake, root, map[string]string{"N/h.txt": "f, synced everywhere", "N/f.txt": "g's bytes, longer"}, fresh, a, b)
	wantSameItem(t, fake, nID, "h.txt", fID)
	wantSameItem(t, fake, nID, "f.txt", gID)
}

// F3: F1 with the forget's DB write failing. The folder's commit depends on
// it and must not run; otherwise it re-homes F's row onto N/f.txt and a later
// cycle uploads unrelated bytes into F. F never receives other bytes.
func TestS031F3FailedForgetBlocksTheFolderCommit(t *testing.T) {
	fake, root, a, b, nID, fID, gID := s031F1(t)
	const edit = "N/f.txt edited on a, not yet synced"
	a.rename(t, "D", "N")
	a.write(t, "N/f.txt", edit)

	executor.FaultHook = func(name string) error {
		if name == executor.CPForgetBeforeCommit {
			return errors.New("injected DB failure at " + name)
		}
		return nil
	}
	res, err := a.eng.Sync(context.Background(), Options{})
	executor.FaultHook = nil
	if err != nil {
		t.Fatalf("[a] cycle with the failing forget: %v", err)
	}
	blocked := false
	for _, e := range res.Errors {
		if strings.HasPrefix(e, "move_local D: not executed") {
			blocked = true
		}
	}
	if !blocked {
		t.Errorf("[a] the folder commit was not blocked by the failed forget; errors: %v", res.Errors)
	}

	settle(t, a, b)
	wantTrees(t, fake, root, map[string]string{"N/h.txt": "f, synced everywhere", "N/f.txt": "g's bytes, longer"}, edit, a, b)
	wantSameItem(t, fake, nID, "h.txt", fID)
	wantSameItem(t, fake, nID, "f.txt", gID)
}

// A move cut after its rename, before its commit; then a new file written at
// the old name — with Drive's item unchanged, and edited meanwhile. F keeps
// its own bytes (or Drive's edit) at g.txt; the new f.txt becomes a new item.
// Was (pre-existing, found by the fuzzer through move_before_commit): the new
// file went up into F, F's bytes to a conflict copy; or, Drive edited, §4.5
// refused every cycle.
func TestS031InterruptedMoveThenNewFileAtTheOldName(t *testing.T) {
	for _, driveEdit := range []bool{false, true} {
		name := "drive unchanged"
		if driveEdit {
			name = "drive edited"
		}
		t.Run(name, func(t *testing.T) {
			fake, root := newWorld(t)
			a := newMachine(t, "a", fake, root)
			b := newMachine(t, "b", fake, root)
			const orig, fresh = "f, synced everywhere", "a new f.txt, written after the crash"
			a.write(t, "f.txt", orig)
			a.sync(t)
			b.sync(t)
			fID := driveFileNamed(t, fake, root, "f.txt")
			b.rename(t, "f.txt", "g.txt")
			b.sync(t)
			crashAtNth(t, a, executor.CPMoveBeforeCommit, 1)
			a.write(t, "f.txt", fresh)
			want := orig
			if driveEdit {
				want = "g edited on b"
				b.write(t, "g.txt", want)
				b.sync(t)
			}
			settleAll(t, a, b)
			wantTrees(t, fake, root, map[string]string{"g.txt": want, "f.txt": fresh}, "", a, b)
			wantSameItem(t, fake, root, "g.txt", fID)
		})
	}
}

// Round 2 (independent check): c.txt renamed to b.txt on Drive (by id) and a
// new c.txt uploaded there; here the same rename and a new c.txt with its own
// bytes. Was: rule O1 refused the plan every cycle (the download of Drive's new
// c.txt was pinned to the local one). Now the two new c.txt meet as §4.2's
// both-new conflict: Drive's at the name, ours beside it as a conflict copy.
func TestS031RotationOnBothSidesTwoMachines(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	const orig, theirs, ours = "c, synced everywhere", "new c from the other machine", "new c written here, only copy"
	a.write(t, "c.txt", orig)
	a.sync(t)
	b.sync(t)
	cID := driveFileNamed(t, fake, root, "c.txt")
	moveOnDrive(t, fake, driveMove{cID, root, "b.txt"})
	if _, err := fake.Upload(context.Background(), root, "c.txt", strings.NewReader(theirs), int64(len(theirs))); err != nil {
		t.Fatal(err)
	}
	a.rename(t, "c.txt", "b.txt")
	a.write(t, "c.txt", ours)

	settleAll(t, a, b)
	wantTrees(t, fake, root, map[string]string{"b.txt": orig, "c.txt": theirs}, ours, a, b)
	wantSameItem(t, fake, root, "b.txt", cID)
}
