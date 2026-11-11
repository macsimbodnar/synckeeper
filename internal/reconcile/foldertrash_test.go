package reconcile

import (
	"reflect"
	"testing"
)

// S021: what a folder TrashRemote expects the folder to hold on Drive when it
// runs. Mirror: d1 "D" holds f1 "a.txt", d2 "s" (holding f2 "b.txt") and g1,
// a Google-native file the snapshot skips; f9 sits beside D.
var pinMirror = []RemoteChild{
	{FileID: "d1", ParentID: "root", Name: "D", IsDir: true},
	{FileID: "f1", ParentID: "d1", Name: "a.txt", MD5: "m1"},
	{FileID: "d2", ParentID: "d1", Name: "s", IsDir: true},
	{FileID: "f2", ParentID: "d2", Name: "b.txt", MD5: "m2"},
	{FileID: "g1", ParentID: "d1", Name: "Notes"},
	{FileID: "f9", ParentID: "root", Name: "x.txt", MD5: "m9"},
}

func pinned(t *testing.T, plan []Action, typ Type, id string) []RemoteChild {
	t.Helper()
	for _, a := range plan {
		if a.Type == typ && a.FileID == id {
			if a.RemoteWant == nil || a.RemoteWant.Contents == nil {
				t.Fatalf("%s %s: contents not pinned", typ, id)
			}
			return a.RemoteWant.Contents.Items
		}
	}
	t.Fatalf("no %s %s in the plan", typ, id)
	return nil
}

func child(id string) RemoteChild {
	for _, c := range pinMirror {
		if c.FileID == id {
			return c
		}
	}
	panic(id)
}

func dirTrash(rel, id string) Action {
	return Action{Type: TrashRemote, RelPath: rel, FileID: id, IsDir: true,
		RemoteWant: &RemoteExpect{ParentID: "root", Name: rel, IsDir: true}}
}

func TestPinFolderTrashContents(t *testing.T) {
	t.Run("collapsed: the whole mirror subtree, skipped rows included", func(t *testing.T) {
		plan := []Action{dirTrash("D", "d1")}
		got := pinned(t, PinFolderTrashContents(plan, pinMirror), TrashRemote, "d1")
		want := []RemoteChild{child("d2"), child("f1"), child("g1"), child("f2")}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("contents = %+v, want %+v", got, want)
		}
	})
	t.Run("uncollapsed: what the plan's own trashes leave, subtrees included", func(t *testing.T) {
		plan := []Action{
			{Type: TrashRemote, RelPath: "D/s/b.txt", FileID: "f2", RemoteWant: &RemoteExpect{}},
			{Type: TrashRemote, RelPath: "D/a.txt", FileID: "f1", RemoteWant: &RemoteExpect{}},
			dirTrash("D/s", "d2"),
			dirTrash("D", "d1"),
		}
		out := PinFolderTrashContents(plan, pinMirror)
		if got := pinned(t, out, TrashRemote, "d2"); len(got) != 0 {
			t.Errorf("D/s contents = %+v, want empty: its one file is trashed first", got)
		}
		if got, want := pinned(t, out, TrashRemote, "d1"), []RemoteChild{child("g1")}; !reflect.DeepEqual(got, want) {
			t.Errorf("D contents = %+v, want %+v: only the row the plan never trashes", got, want)
		}
		for _, a := range out[:2] {
			if a.RemoteWant.Contents != nil {
				t.Errorf("file trash %s got contents pinned", a.RelPath)
			}
		}
	})
	t.Run("a subfolder collapsed under an uncollapsed parent is taken out whole", func(t *testing.T) {
		sub := dirTrash("D/s", "d2")
		sub.Subtree = []SubtreeEntry{{RelPath: "D/s/b.txt", FileID: "f2"}}
		plan := []Action{
			{Type: MoveRemote, RelPath: "D/a.txt", NewRelPath: "a.txt", FileID: "f1", RemoteWant: &RemoteExpect{}},
			sub,
			dirTrash("D", "d1"),
		}
		out := PinFolderTrashContents(plan, pinMirror)
		if got, want := pinned(t, out, TrashRemote, "d2"), []RemoteChild{child("f2")}; !reflect.DeepEqual(got, want) {
			t.Errorf("D/s contents = %+v, want %+v", got, want)
		}
		if got, want := pinned(t, out, TrashRemote, "d1"), []RemoteChild{child("g1")}; !reflect.DeepEqual(got, want) {
			t.Errorf("D contents = %+v, want %+v: a.txt moved out, s trashed first", got, want)
		}
	})
	t.Run("the input plan is not written through", func(t *testing.T) {
		plan := []Action{dirTrash("D", "d1"), {Type: TrashRemote, RelPath: "E", FileID: "e1", IsDir: true}}
		out := PinFolderTrashContents(plan, pinMirror)
		if plan[0].RemoteWant.Contents != nil {
			t.Error("the input action's RemoteWant was modified")
		}
		if out[1].RemoteWant != nil {
			t.Error("an action with no RemoteWant got one")
		}
	})
}

// Both shapes as the planner and the collapse actually produce them: a
// folder deleted locally, its Drive copy unchanged.
func TestFolderTrashShapesPinWhatThePlanLeaves(t *testing.T) {
	in := Input{
		Base: map[string]BaseItem{
			"D": baseDir("d1"), "D/a.txt": baseFile("f1", "m1", 1),
			"D/s": baseDir("d2"), "D/s/b.txt": baseFile("f2", "m2", 1),
		},
		Local: map[string]LocalItem{},
		Remote: map[string]RemoteItem{
			"D": remIn(remDir("d1", 1), "root"), "D/a.txt": remIn(remFile("f1", "m1", 1, 1), "d1"),
			"D/s": remIn(remDir("d2", 1), "d1"), "D/s/b.txt": remIn(remFile("f2", "m2", 1, 1), "d2"),
		},
	}
	plan, _ := Plan(in)

	collapsed := PinFolderTrashContents(CollapseDirDeletes(plan, in.Remote), pinMirror)
	if len(collapsed) != 1 {
		t.Fatalf("collapsed plan = %+v, want one folder trash", collapsed)
	}
	if got := pinned(t, collapsed, TrashRemote, "d1"); len(got) != 4 {
		t.Errorf("collapsed D contents = %+v, want its 4 mirror rows", got)
	}

	uncollapsed := PinFolderTrashContents(plan, pinMirror)
	if got := pinned(t, uncollapsed, TrashRemote, "d2"); len(got) != 0 {
		t.Errorf("uncollapsed D/s contents = %+v, want empty", got)
	}
	if got, want := pinned(t, uncollapsed, TrashRemote, "d1"), []RemoteChild{child("g1")}; !reflect.DeepEqual(got, want) {
		t.Errorf("uncollapsed D contents = %+v, want %+v", got, want)
	}
}
