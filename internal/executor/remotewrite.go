package executor

// The remote-write gate (spec §7, S010): the remote counterpart of
// localwrite.go, and the ONLY file in the package permitted to call a Drive
// mutation on an existing item. Each one carries the Drive state its plan was
// built on (Action.RemoteWant) as a driveclient.Precondition; the client
// re-reads the item immediately before mutating it and refuses on drift. A
// refusal is an ordinary failed action — counted, logged, the journal left
// for the next cycle to discard — and the replan sees the other machine's
// write as the decision table's conflict or edit-beats-delete row instead of
// overwriting it. The companion AST test (remotegate_test.go) enforces the
// exclusivity, as gate_test.go does for local paths.
//
// A folder trash is checked twice (S021): its contents here, against Drive's
// listing, then the folder itself in the client. Trashing a folder bins its
// whole subtree, and the folder's own pin says nothing about what is inside.

import (
	"context"
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// remoteGate attaches a's expected Drive state to ctx. An action without one
// — journaled before RemoteWant existed, or built by hand in a test — runs
// unchecked, as every mutation did before S010.
func remoteGate(ctx context.Context, a reconcile.Action) context.Context {
	w := a.RemoteWant
	if w == nil {
		return ctx
	}
	return driveclient.WithPrecondition(ctx, driveclient.Precondition{
		FileID: a.FileID, ParentID: w.ParentID, Name: w.Name, IsDir: w.IsDir, MD5: w.MD5,
	})
}

func (x *Executor) gatedUpdate(ctx context.Context, a reconcile.Action, content io.Reader, size int64) (driveclient.File, error) {
	return x.Client.Update(remoteGate(ctx, a), a.FileID, content, size)
}

func (x *Executor) gatedTrash(ctx context.Context, a reconcile.Action) error {
	if err := x.verifyFolderContents(ctx, a); err != nil {
		return err
	}
	return x.Client.Trash(remoteGate(ctx, a), a.FileID)
}

// folderListWorkers bounds the folder-trash gate's concurrent listings. It is
// the executor's own limit on concurrent Drive calls (transferWorkers): the
// delete stage is serial, so the walk is the only Drive traffic while it runs.
// A var so tests can compare against the serial walk.
var folderListWorkers = transferWorkers

// verifyFolderContents lists the folder a TrashRemote is about to bin and
// each expected subfolder, and refuses with driveclient.ErrDrift unless Drive
// holds exactly a.RemoteWant.Contents.Items: every expected item at its planned
// parent, name, type and md5, and nothing else. One List per expected folder
// (more pages past 1000 children). No call at all when the contents were not
// pinned.
//
// Order: the expected subfolders deepest first, folderListWorkers at a time,
// then — after every one of them has returned — the folder itself, last, so
// the level nearest the user gets the freshest listing. An unexpected
// subfolder is caught at its parent's listing and never walked. The first
// drift or error stops the walk; the folder itself is then never listed.
//
// What it cannot see: a write landing in a folder after that folder's List
// and before the Trash lands — for a subfolder, the rest of the walk; for the
// folder itself, the client's Get and the trash request (with withRetry's
// backoff when it is retried). Such a write still goes to Drive's bin with
// the folder. No conditional write closes it.
func (x *Executor) verifyFolderContents(ctx context.Context, a reconcile.Action) error {
	w := a.RemoteWant
	if w == nil || w.Contents == nil {
		return nil
	}
	items := w.Contents.Items
	drift := func(what string) error {
		return fmt.Errorf("%w: folder %s %s; leaving it alone, replanning next cycle", driveclient.ErrDrift, a.FileID, what)
	}
	want := make(map[string]reconcile.RemoteChild, len(items))
	for _, c := range items {
		want[c.FileID] = c
	}
	depth := func(id string) int {
		d := 0
		for c, ok := want[id]; ok && d <= len(want); c, ok = want[c.ParentID] {
			d++
		}
		return d
	}
	var subs []string
	for _, c := range items {
		if c.IsDir {
			subs = append(subs, c.FileID)
		}
	}
	sort.SliceStable(subs, func(i, j int) bool { return depth(subs[i]) > depth(subs[j]) })

	var mu sync.Mutex
	seen := map[string]bool{}
	listOne := func(ctx context.Context, parent string) error {
		kids, err := x.Client.List(ctx, parent)
		if err != nil {
			if driveclient.IsNotFound(err) {
				return drift(fmt.Sprintf("folder %s gone from Drive", parent))
			}
			return fmt.Errorf("drive write gate: listing %s before trashing folder %s: %w", parent, a.FileID, err)
		}
		mu.Lock()
		defer mu.Unlock()
		for _, k := range kids {
			c, ok := want[k.ID]
			switch {
			case !ok:
				return drift(fmt.Sprintf("holds %q (%s), which the plan never saw", k.Name, k.ID))
			case seen[k.ID]:
				return drift(fmt.Sprintf("holds %q (%s) in two folders", k.Name, k.ID))
			case c.ParentID != parent:
				return drift(fmt.Sprintf("has %q (%s) in folder %s (planned %s)", k.Name, k.ID, parent, c.ParentID))
			case k.Name != c.Name:
				return drift(fmt.Sprintf("has %s renamed to %q (planned %q)", k.ID, k.Name, c.Name))
			case k.IsDir() != c.IsDir:
				return drift(fmt.Sprintf("has %q (%s) changed type", k.Name, k.ID))
			case !c.IsDir && k.MD5 != c.MD5:
				return drift(fmt.Sprintf("has %q (%s) with new content (md5 %s, planned %s)", k.Name, k.ID, k.MD5, c.MD5))
			}
			seen[k.ID] = true
		}
		return nil
	}

	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var firstErr error
	jobs := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < min(folderListWorkers, len(subs)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for parent := range jobs {
				if wctx.Err() != nil {
					continue // stopped: drain without listing
				}
				if err := listOne(wctx, parent); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					cancel()
				}
			}
		}()
	}
feed:
	for _, parent := range subs {
		select {
		case jobs <- parent:
		case <-wctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := listOne(ctx, a.FileID); err != nil {
		return err
	}

	if len(seen) != len(want) {
		for _, c := range items {
			if !seen[c.FileID] {
				return drift(fmt.Sprintf("no longer holds %q (%s): trashed, moved out or gone (%d of %d planned items missing)",
					c.Name, c.FileID, len(want)-len(seen), len(want)))
			}
		}
	}
	return nil
}

func (x *Executor) gatedMove(ctx context.Context, a reconcile.Action, parentID, name string) (driveclient.File, error) {
	return x.Client.Move(remoteGate(ctx, a), a.FileID, parentID, name)
}
