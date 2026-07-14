package engine

// Phase 4: init --adopt merges a non-empty Drive folder with a non-empty
// local dir. Adoption is just the first sync over an empty baseline, so these
// tests drive the engine the same way `init --adopt` does.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// assertNoDeletes fails if a plan contains any delete-class action — the core
// adopt guarantee (an empty baseline can't produce deletes, but assert it).
func assertNoDeletes(t *testing.T, res *Result) {
	t.Helper()
	for _, a := range res.Plan {
		if a.Type == reconcile.TrashRemote || a.Type == reconcile.QuarantineLocal || a.Type == reconcile.Forget {
			t.Fatalf("adopt merge produced a delete-class action: %s %s", a.Type, a.RelPath)
		}
	}
}

// Machine B joins an existing Drive folder: remote-only files download,
// local-only files upload, identical files adopt, and nothing is deleted.
func TestAdoptUnionMerge(t *testing.T) {
	fake, root := newWorld(t)

	a := newMachine(t, "a", fake, root)
	a.write(t, "shared.txt", "identical")
	a.write(t, "only_a.txt", "from A")
	a.write(t, "docs/guide.md", "A guide")
	a.sync(t) // populate Drive

	b := newMachine(t, "b", fake, root)
	b.write(t, "shared.txt", "identical") // same md5 -> adopt, no transfer
	b.write(t, "only_b.txt", "from B")    // local-only -> upload

	res := b.sync(t) // the adopt merge
	assertNoDeletes(t, res)

	// B now has the union.
	if got := b.read(t, "shared.txt"); got != "identical" {
		t.Errorf("shared.txt = %q", got)
	}
	if got := b.read(t, "only_a.txt"); got != "from A" {
		t.Errorf("only_a.txt = %q (remote-only should have downloaded)", got)
	}
	if got := b.read(t, "docs/guide.md"); got != "A guide" {
		t.Errorf("docs/guide.md = %q", got)
	}
	if !b.exists("only_b.txt") {
		t.Error("only_b.txt missing on B")
	}

	// A converges after its next sync (picks up only_b.txt).
	a.sync(t)
	assertConverged(t, a, b)
}

// Same path, different content on each side becomes a conflict copy: remote
// wins the canonical name, the local version is preserved as a conflict copy,
// and nothing is lost.
func TestAdoptConflictsOnDivergentContent(t *testing.T) {
	fake, root := newWorld(t)

	a := newMachine(t, "a", fake, root)
	a.write(t, "notes.txt", "A version")
	a.sync(t)

	b := newMachine(t, "b", fake, root)
	b.write(t, "notes.txt", "B version") // diverges from A's remote copy
	res := b.sync(t)
	assertNoDeletes(t, res)

	// Remote (A) wins the canonical name.
	if got := b.read(t, "notes.txt"); got != "A version" {
		t.Errorf("notes.txt = %q, want A's remote version to win canonical", got)
	}
	// B's version survives as a conflict copy.
	if !hasConflictCopyWith(t, b.dir, "B version") {
		t.Error("B's version was not preserved as a conflict copy")
	}

	// Everyone converges.
	a.sync(t)
	b.sync(t)
	assertConverged(t, a, b)
}

// Three machines with offline concurrent edits all converge to identical
// trees with no version silently lost.
func TestThreeMachineConvergence(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	c := newMachine(t, "c", fake, root)

	a.write(t, "file.txt", "base")
	a.sync(t)
	b.sync(t) // pull base
	c.sync(t)

	// Offline concurrent divergent edits on the same file.
	a.write(t, "file.txt", "A edit")
	b.write(t, "file.txt", "B edit")
	c.write(t, "file.txt", "C edit")

	// They sync in sequence; later ones conflict against the winner.
	a.sync(t)
	b.sync(t)
	c.sync(t)

	// Drain to a fixed point.
	for i := 0; i < 4; i++ {
		a.sync(t)
		b.sync(t)
		c.sync(t)
	}
	assertConverged(t, a, b, c)

	// No edit was lost: every distinct version survives somewhere in the tree.
	tree := a.listTree(t)
	for _, want := range []string{"A edit", "B edit", "C edit"} {
		if !treeContains(tree, want) {
			t.Errorf("version %q was lost (not present anywhere after convergence)", want)
		}
	}
}

// A third machine adopts an already-active folder (A and B have been syncing)
// and everyone converges, its own local file included.
func TestAdoptWhileOthersActive(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)

	a.write(t, "doc.txt", "shared doc")
	a.sync(t)
	b.sync(t)
	b.write(t, "b_only.txt", "from B")
	b.sync(t)
	a.sync(t) // A and B are converged and active

	// C joins now via adopt, bringing a local-only file.
	c := newMachine(t, "c", fake, root)
	c.write(t, "c_only.txt", "from C")
	res := c.sync(t)
	assertNoDeletes(t, res)

	for i := 0; i < 3; i++ {
		a.sync(t)
		b.sync(t)
		c.sync(t)
	}
	assertConverged(t, a, b, c)
	if !c.exists("doc.txt") || !c.exists("b_only.txt") || !c.exists("c_only.txt") {
		t.Error("C did not end with the full union after adopting")
	}
}

func hasConflictCopyWith(t *testing.T, dir, content string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.Contains(e.Name(), "conflict") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err == nil && string(raw) == content {
			return true
		}
	}
	return false
}

func treeContains(tree map[string]string, content string) bool {
	for _, v := range tree {
		if v == content {
			return true
		}
	}
	return false
}
