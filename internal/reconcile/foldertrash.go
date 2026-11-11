package reconcile

// Folder-trash contents (S021, spec §7). Trashing a Drive folder takes its
// whole subtree to Drive's bin, so the remote-write gate's check of the
// folder itself (name, parent, not trashed — S010) is not enough: a file
// another machine edited or added inside after this machine's refresh would
// ride along, and then leave that machine's disk. A folder TrashRemote
// therefore carries what the folder may still hold when the trash runs, and
// the executor compares it with Drive's listing immediately before the trash.
// The local counterpart is W13-T2's Subtree + guardedTrashDir.

import "sort"

// PinFolderTrashContents sets RemoteWant.Contents on every folder TrashRemote
// of a finished plan — after CollapseDirDeletes, when it runs — from mirror,
// the non-trashed rows of the remote mirror the plan was built on.
//
// The expectation is the folder's mirror subtree minus every item an earlier
// action of the same plan trashes or moves out, with its own subtree. The
// delete stage runs bottom-up and serially, so those actions have run when
// the folder's turn comes; one that failed leaves its item in place, and the
// gate then finds something it does not expect and refuses — the folder never
// takes along an item whose own action was refused (invariant 7). The two
// shapes fall out of the one rule:
//
//   - collapsed (one action stands for the subtree): nothing under the folder
//     has an action of its own, so the expectation is the whole subtree, each
//     item at the parent, name and md5 the mirror holds;
//   - uncollapsed (per-item trashes, then the folder): every tracked item has
//     its own trash or move, so the expectation is what the plan leaves
//     there — normally nothing; only rows the snapshot skips (Google-native,
//     ignored, invalid or shadowed names) remain, as they always went to the
//     bin with the folder.
//
// Pure; returns a new plan, and the RemoteWant it changes is copied, never
// written through.
func PinFolderTrashContents(plan []Action, mirror []RemoteChild) []Action {
	children := map[string][]RemoteChild{}
	for _, n := range mirror {
		children[n.ParentID] = append(children[n.ParentID], n)
	}
	for _, kids := range children {
		sort.Slice(kids, func(i, j int) bool { return kids[i].FileID < kids[j].FileID })
	}
	takenOut := map[string]bool{}
	for _, a := range plan {
		if a.Type == TrashRemote || a.Type == MoveRemote {
			takenOut[a.FileID] = true
		}
	}

	out := make([]Action, len(plan))
	copy(out, plan)
	for i, a := range out {
		if a.Type != TrashRemote || !a.IsDir || a.RemoteWant == nil {
			continue
		}
		var contents []RemoteChild
		visited := map[string]bool{a.FileID: true}
		queue := []string{a.FileID}
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			for _, c := range children[id] {
				if takenOut[c.FileID] || visited[c.FileID] {
					continue // gone before the folder's turn, subtree and all
				}
				visited[c.FileID] = true
				contents = append(contents, c)
				if c.IsDir {
					queue = append(queue, c.FileID)
				}
			}
		}
		want := *a.RemoteWant
		want.Contents = &RemoteContents{Items: contents}
		out[i].RemoteWant = &want
	}
	return out
}
