package reconcile

import "testing"

// W13-T2: a folder whose entire subtree is being deleted becomes ONE action
// carrying the subtree, so the user's bin gets one restorable folder instead
// of a thousand loose files.
func TestCollapseAbsorbsFullyDeletedSubtree(t *testing.T) {
	plan := []Action{
		{Type: QuarantineLocal, RelPath: "pack/vec/a.svg", FileID: "a", LocalExists: true, LocalSize: 3, LocalMtimeNS: 11},
		{Type: QuarantineLocal, RelPath: "pack/vec/b.svg", FileID: "b", LocalExists: true, LocalSize: 4, LocalMtimeNS: 22},
		{Type: QuarantineLocal, RelPath: "pack/vec", FileID: "vec", IsDir: true},
		{Type: QuarantineLocal, RelPath: "pack", FileID: "pack", IsDir: true},
	}
	got := CollapseDirDeletes(plan, nil)
	if len(got) != 1 {
		t.Fatalf("plan = %d actions, want 1 collapsed folder delete: %+v", len(got), got)
	}
	a := got[0]
	if a.RelPath != "pack" || !a.IsDir {
		t.Fatalf("collapsed action = %+v, want the highest directory", a)
	}
	if a.SubtreeFiles != 2 {
		t.Errorf("SubtreeFiles = %d, want 2 — counting must survive the collapse", a.SubtreeFiles)
	}
	if len(a.Subtree) != 3 {
		t.Fatalf("Subtree = %+v, want the two files and the inner directory", a.Subtree)
	}
	for _, e := range a.Subtree {
		if e.RelPath == "pack/vec/a.svg" && (e.Size != 3 || e.MtimeNS != 11 || e.FileID != "a") {
			t.Errorf("entry lost the scan's pin: %+v", e)
		}
	}
}

// The collapse is only safe when the plan has no other business inside the
// folder: one upload beneath it and every delete goes back to item by item.
func TestCollapseRefusedWhenSubtreeHasOtherWork(t *testing.T) {
	for _, other := range []Action{
		{Type: Upload, RelPath: "pack/vec/new.svg"},
		{Type: Download, RelPath: "pack/vec/new.svg"},
		{Type: Record, RelPath: "pack/vec/a.svg"},
		{Type: MoveLocal, RelPath: "elsewhere.txt", NewRelPath: "pack/vec/moved.txt"},
	} {
		plan := []Action{
			other,
			{Type: QuarantineLocal, RelPath: "pack/vec/a.svg", FileID: "a"},
			{Type: QuarantineLocal, RelPath: "pack", FileID: "pack", IsDir: true},
		}
		got := CollapseDirDeletes(plan, nil)
		if len(got) != len(plan) {
			t.Errorf("%s under the folder must block the collapse, got %+v", other.Type, got)
		}
	}
}

// Remote-side deletes and row drops beneath the folder are still deletes:
// they touch no local path the bin move would surprise.
func TestCollapseAllowsOtherDeleteClassActions(t *testing.T) {
	plan := []Action{
		{Type: TrashRemote, RelPath: "pack/vec/gone.svg", FileID: "g"},
		{Type: Forget, RelPath: "pack/vec/old.svg", FileID: "o"},
		{Type: QuarantineLocal, RelPath: "pack/vec/a.svg", FileID: "a"},
		{Type: QuarantineLocal, RelPath: "pack", FileID: "pack", IsDir: true},
	}
	got := CollapseDirDeletes(plan, nil)
	if len(got) != 3 {
		t.Fatalf("plan = %+v, want the trash_remote, the forget, and one collapsed delete", got)
	}
	if got[2].SubtreeFiles != 1 {
		t.Errorf("SubtreeFiles = %d, want 1", got[2].SubtreeFiles)
	}
}

// The highest qualifying directory wins: one bin entry for the whole tree,
// not one per level.
func TestCollapseKeepsOnlyTheHighestDirectory(t *testing.T) {
	plan := []Action{
		{Type: QuarantineLocal, RelPath: "a/b/c/f.txt", FileID: "f"},
		{Type: QuarantineLocal, RelPath: "a/b/c", FileID: "c", IsDir: true},
		{Type: QuarantineLocal, RelPath: "a/b", FileID: "b", IsDir: true},
		{Type: QuarantineLocal, RelPath: "a", FileID: "a", IsDir: true},
	}
	got := CollapseDirDeletes(plan, nil)
	if len(got) != 1 || got[0].RelPath != "a" {
		t.Fatalf("got %+v, want only the top directory", got)
	}
	if got[0].SubtreeFiles != 1 || len(got[0].Subtree) != 3 {
		t.Errorf("collapsed action = %+v, want 1 file and 3 covered entries", got[0])
	}
}

// A directory delete with nothing beneath it is left exactly as it was —
// no Subtree, so the executor takes the ordinary empty-directory road.
func TestCollapseLeavesLoneDirectoryUntouched(t *testing.T) {
	plan := []Action{{Type: QuarantineLocal, RelPath: "empty", FileID: "e", IsDir: true}}
	got := CollapseDirDeletes(plan, nil)
	if len(got) != 1 || got[0].Subtree != nil || got[0].SubtreeFiles != 0 {
		t.Fatalf("got %+v, want the action unchanged", got)
	}
}

// Sibling folders collapse independently, and deletes outside any collapsing
// folder are untouched.
func TestCollapseHandlesSiblingsAndLooseFiles(t *testing.T) {
	plan := []Action{
		{Type: QuarantineLocal, RelPath: "loose.txt", FileID: "l"},
		{Type: QuarantineLocal, RelPath: "one/f.txt", FileID: "1f"},
		{Type: QuarantineLocal, RelPath: "one", FileID: "1", IsDir: true},
		{Type: QuarantineLocal, RelPath: "two/f.txt", FileID: "2f"},
		{Type: QuarantineLocal, RelPath: "two", FileID: "2", IsDir: true},
	}
	got := CollapseDirDeletes(plan, nil)
	if len(got) != 3 {
		t.Fatalf("got %+v, want the loose file plus one action per folder", got)
	}
	for _, a := range got {
		if a.IsDir && a.SubtreeFiles != 1 {
			t.Errorf("%s covers %d files, want 1", a.RelPath, a.SubtreeFiles)
		}
	}
}

// W14-M4: the mirror image on the Drive side — a locally-deleted folder is
// trashed as ONE Drive item, which is one API call instead of one per file
// and one restorable entry in Drive's bin instead of a thousand.
func TestCollapseAbsorbsRemoteTrashSubtree(t *testing.T) {
	plan := []Action{
		{Type: TrashRemote, RelPath: "pack/vec/a.svg", FileID: "a"},
		{Type: TrashRemote, RelPath: "pack/vec/b.svg", FileID: "b"},
		{Type: TrashRemote, RelPath: "pack/vec", FileID: "vec", IsDir: true},
		{Type: TrashRemote, RelPath: "pack", FileID: "pack", IsDir: true},
	}
	remote := map[string]RemoteItem{
		"pack":           {FileID: "pack", IsDir: true},
		"pack/vec":       {FileID: "vec", IsDir: true},
		"pack/vec/a.svg": {FileID: "a"},
		"pack/vec/b.svg": {FileID: "b"},
	}
	got := CollapseDirDeletes(plan, remote)
	if len(got) != 1 || got[0].RelPath != "pack" || got[0].Type != TrashRemote {
		t.Fatalf("got %+v, want one trash_remote for the folder", got)
	}
	if got[0].SubtreeFiles != 2 || len(got[0].Subtree) != 3 {
		t.Errorf("collapsed action = %+v, want 2 files and 3 covered entries", got[0])
	}
}

// Trashing a folder on Drive takes everything under it. A remote item the
// plan never planned to delete — a file another machine just added, one the
// scanner skipped — must not be swept along, so the folder is trashed the
// long way instead.
func TestCollapseRefusedWhenDriveHoldsAnUnplannedItem(t *testing.T) {
	plan := []Action{
		{Type: TrashRemote, RelPath: "pack/a.svg", FileID: "a"},
		{Type: TrashRemote, RelPath: "pack", FileID: "pack", IsDir: true},
	}
	remote := map[string]RemoteItem{
		"pack":         {FileID: "pack", IsDir: true},
		"pack/a.svg":   {FileID: "a"},
		"pack/new.svg": {FileID: "n"}, // nobody planned anything for this
	}
	if got := CollapseDirDeletes(plan, remote); len(got) != len(plan) {
		t.Errorf("got %+v, want the plan untouched — a stranger lives under the folder", got)
	}
}

// A forgotten row (gone on both sides) is not a survivor: it is content the
// plan already accounts for.
func TestCollapseRemoteAllowsForgottenRows(t *testing.T) {
	plan := []Action{
		{Type: Forget, RelPath: "pack/old.svg", FileID: "o"},
		{Type: TrashRemote, RelPath: "pack/a.svg", FileID: "a"},
		{Type: TrashRemote, RelPath: "pack", FileID: "pack", IsDir: true},
	}
	remote := map[string]RemoteItem{
		"pack":         {FileID: "pack", IsDir: true},
		"pack/a.svg":   {FileID: "a"},
		"pack/old.svg": {FileID: "o"},
	}
	got := CollapseDirDeletes(plan, remote)
	if len(got) != 2 {
		t.Fatalf("got %+v, want the forget plus one collapsed trash_remote", got)
	}
}

// The two arms never absorb each other: a local removal and a Drive removal
// move different things, and a subtree mixing them means the sides disagree
// about what is gone.
func TestCollapseKeepsTheTwoDirectionsApart(t *testing.T) {
	plan := []Action{
		{Type: QuarantineLocal, RelPath: "pack/local.txt", FileID: "l"},
		{Type: TrashRemote, RelPath: "pack", FileID: "pack", IsDir: true},
	}
	remote := map[string]RemoteItem{"pack": {FileID: "pack", IsDir: true}}
	got := CollapseDirDeletes(plan, remote)
	if len(got) != 2 {
		t.Fatalf("got %+v, want both actions kept", got)
	}
	for _, a := range got {
		if a.SubtreeFiles != 0 {
			t.Errorf("%s absorbed across directions: %+v", a.RelPath, a)
		}
	}
}
