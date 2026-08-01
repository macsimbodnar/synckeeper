package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
)

// W18.7 end to end — `init`'s merge gives the plain name to the side edited
// last (spec §11), and the loser survives on BOTH sides under the conflict
// name. The pure-planner cases live in internal/reconcile; these two run the
// whole cycle against the fake Drive and then let the other machine converge,
// which is where a merely plausible plan shape would show up as a duplicate
// name on Drive or a file only one machine can see.

// mergeSync is the cycle `init` runs: the only caller that sets PreferNewer.
func (m *machine) mergeSync(t *testing.T) *Result {
	t.Helper()
	res, err := m.eng.Sync(context.Background(), Options{PreferNewer: true})
	if err != nil {
		t.Fatalf("[%s] merge: %v", m.name, err)
	}
	if res.Failed > 0 {
		t.Fatalf("[%s] merge had %d failed actions: %v", m.name, res.Failed, res.Errors)
	}
	assertMirrorCoversBaseline(t, m)
	return res
}

// driveFileNames is every non-trashed file name under a Drive folder. Same-name
// siblings are what this returns as duplicates, which is the point.
func driveFileNames(t *testing.T, fake *driveclient.Fake, folderID string) []string {
	t.Helper()
	children, err := fake.List(context.Background(), folderID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range children {
		out = append(out, c.Name)
	}
	return out
}

// conflictCopyOf finds the one conflict copy in a machine's tree.
func conflictCopyOf(t *testing.T, m *machine, stem string) string {
	t.Helper()
	var found []string
	for p := range m.listTree(t) {
		if p != stem && filepath.Ext(p) == filepath.Ext(stem) && p != stem {
			found = append(found, p)
		}
	}
	if len(found) != 1 {
		t.Fatalf("[%s] want exactly one conflict copy beside %s, got %v", m.name, stem, found)
	}
	return found[0]
}

// The case E exists for: a machine joins carrying work newer than the copy on
// Drive. Before W18-E that work was demoted to a conflict copy and the older
// Drive content took the plain name.
func TestInitMergeKeepsTheNewerLocalFileUnderThePlainName(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)

	// Drive's copy is two hours old.
	stale := time.Now().Add(-2 * time.Hour)
	fake.Now = func() time.Time { return stale }
	b.write(t, "notes.txt", "older, from drive")
	b.sync(t)
	fake.Now = nil

	// The joining machine's own file is current.
	a.write(t, "notes.txt", "newer, edited here")
	a.mergeSync(t)

	if got := a.read(t, "notes.txt"); got != "newer, edited here" {
		t.Errorf("notes.txt on the joining machine = %q, want the newer local content", got)
	}
	cp := conflictCopyOf(t, a, "notes.txt")
	if got := a.read(t, cp); got != "older, from drive" {
		t.Errorf("%s = %q, want Drive's older content preserved", cp, got)
	}

	// Drive holds both, under two distinct names. Two items under ONE name in
	// one folder is legal on Drive and is exactly the W17 shadowing shape, so
	// the count is asserted, not just the presence.
	names := driveFileNames(t, fake, root)
	if len(names) != 2 {
		t.Fatalf("Drive folder holds %v, want exactly two distinctly-named files", names)
	}
	if names[0] == names[1] {
		t.Fatalf("Drive folder holds two items under one name: %v", names)
	}

	// The machine that had the older copy converges on the same answer.
	b.sync(t)
	assertConverged(t, a, b)
	if got := b.read(t, "notes.txt"); got != "newer, edited here" {
		t.Errorf("[b] notes.txt = %q, want the newer content under the plain name", got)
	}
	if got := b.read(t, cp); got != "older, from drive" {
		t.Errorf("[b] %s = %q, want the older content under the conflict name", cp, got)
	}

	// Idempotent: a second merge over the settled state does nothing.
	if res := a.mergeSync(t); len(res.Plan) != 0 {
		t.Errorf("re-running the merge planned %d actions, want 0: %+v", len(res.Plan), res.Plan)
	}
}

// The other direction is unchanged behaviour, asserted so the new rule is
// visibly a rule and not a flip: when Drive holds the newer copy, Drive keeps
// the plain name exactly as it always did.
func TestInitMergeLeavesTheStaleLocalFileAsTheConflictCopy(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)

	b.write(t, "notes.txt", "newer, from drive")
	b.sync(t)

	// The joining machine's file has not been touched in a week.
	a.write(t, "notes.txt", "older, edited here")
	stale := time.Now().Add(-7 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(a.dir, "notes.txt"), stale, stale); err != nil {
		t.Fatal(err)
	}
	a.mergeSync(t)

	if got := a.read(t, "notes.txt"); got != "newer, from drive" {
		t.Errorf("notes.txt = %q, want Drive's newer content under the plain name", got)
	}
	cp := conflictCopyOf(t, a, "notes.txt")
	if got := a.read(t, cp); got != "older, edited here" {
		t.Errorf("%s = %q, want the stale local content preserved", cp, got)
	}
	b.sync(t)
	assertConverged(t, a, b)
}

// Steady-state syncing must not consult the clock at all: the same "local is
// newer" input that flips the winner at a merge leaves Drive holding the plain
// name during ordinary cycles. Remote-wins is the rule that works without
// cross-machine clock agreement, and W18-E does not touch it.
func TestOrdinarySyncStillGivesThePlainNameToDrive(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)

	stale := time.Now().Add(-2 * time.Hour)
	fake.Now = func() time.Time { return stale }
	b.write(t, "notes.txt", "older, from drive")
	b.sync(t)
	fake.Now = nil

	a.write(t, "notes.txt", "newer, edited here")
	a.sync(t) // no PreferNewer

	if got := a.read(t, "notes.txt"); got != "older, from drive" {
		t.Errorf("notes.txt = %q, want Drive's content — steady state is remote-wins", got)
	}
}

// A conflict inside a folder the same merge is adopting. The Drive-side rename
// runs in the moves stage, before the Record that adopts the folder, so this is
// the end-to-end witness for the parent-id fix in the executor
// (TestFolderIDsComeFromThePlanNotTheBaseline).
func TestInitMergeResolvesAConflictInsideAnAdoptedFolder(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)

	stale := time.Now().Add(-2 * time.Hour)
	fake.Now = func() time.Time { return stale }
	b.write(t, "docs/notes.txt", "older, from drive")
	b.sync(t)
	fake.Now = nil

	a.write(t, "docs/notes.txt", "newer, edited here")
	a.write(t, "docs/only-here.txt", "local only")
	a.mergeSync(t)

	if got := a.read(t, "docs/notes.txt"); got != "newer, edited here" {
		t.Errorf("docs/notes.txt = %q, want the newer local content", got)
	}
	b.sync(t)
	assertConverged(t, a, b)

	// One "docs" on Drive, holding three distinctly-named files.
	children, err := fake.List(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 1 || !children[0].IsDir() {
		t.Fatalf("Drive root should hold exactly one folder, got %+v", children)
	}
	names := driveFileNames(t, fake, children[0].ID)
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			t.Fatalf("docs/ on Drive holds two items under one name: %v", names)
		}
		seen[n] = true
	}
	if len(names) != 3 {
		t.Errorf("docs/ on Drive holds %v, want three files", names)
	}
}
