package reconcile

// The delete-stage collapse (W13-T2). A folder deleted in Drive must arrive
// in the user's system bin as ONE restorable entry, not as one entry per
// file: the field incident that motivated W13 was a 1145-file folder, and a
// bin holding 1145 loose files is not a folder the user can put back.
//
// The pass is pure and runs on a finished plan, deliberately AFTER the
// mass-delete guard has counted it (spec §6, R10): the guard counts delete
// -class files, and collapsing 1145 file deletes into one directory action
// would make it count zero exactly when it matters most — the mirror image
// of A2, where counting containers instead of content wedged the daemon.
// SubtreeFiles keeps every later count honest.

import (
	"sort"
	"strings"
)

// CollapseDirDeletes absorbs a directory delete's descendant deletes into the
// directory action itself, so one move retires the whole subtree — locally to
// the bin (QuarantineLocal, W13-T2), and on Drive to Drive's bin
// (TrashRemote, W14-M4: one API call and one restorable entry instead of one
// per file). A directory qualifies only when EVERY action the plan makes
// under it is delete-class: any upload, download, move, or record beneath it
// means the plan still has business inside the subtree, and it is deleted
// item by item as before. The highest qualifying directory wins.
//
// remote is the remote snapshot: a directory whose Drive-side deletion would
// take an item the plan never accounted for is not collapsed — the local arm
// re-verifies against the disk at execution time, and this is that check's
// remote counterpart, made here because the mirror is already in hand.
//
// The returned plan keeps every non-absorbed action, in order. The absorbed
// deletes live on as the survivor's Subtree, each carrying the stat the scan
// pinned, so the executor can still refuse a subtree that changed under it.
func CollapseDirDeletes(plan []Action, remote map[string]RemoteItem) []Action {
	var dirs []string
	for _, a := range plan {
		if (a.Type == QuarantineLocal || a.Type == TrashRemote) && a.IsDir {
			dirs = append(dirs, a.RelPath)
		}
	}
	if len(dirs) == 0 {
		return plan
	}
	// Shallowest first, so an ancestor claims the subtree before its children
	// get the chance.
	sort.Slice(dirs, func(i, j int) bool {
		if d1, d2 := strings.Count(dirs[i], "/"), strings.Count(dirs[j], "/"); d1 != d2 {
			return d1 < d2
		}
		return dirs[i] < dirs[j]
	})

	// A root absorbs only deletes of its own kind: the two arms move
	// different things (this machine's copy vs Drive's), and a mixed subtree
	// means the two sides disagree about what is gone — not a case to
	// shortcut.
	kindOf := map[string]Type{}
	for _, a := range plan {
		if (a.Type == QuarantineLocal || a.Type == TrashRemote) && a.IsDir {
			kindOf[a.RelPath] = a.Type
		}
	}
	var roots []string
	for _, d := range dirs {
		if _, absorbed := rootFor(roots, d); absorbed {
			continue // an ancestor already stands for this directory
		}
		if !deletesOnlyUnder(plan, d) {
			continue
		}
		if kindOf[d] == TrashRemote && !remoteFullyCovered(plan, remote, d) {
			continue // Drive holds something under it the plan never planned to delete
		}
		roots = append(roots, d)
	}
	if len(roots) == 0 {
		return plan
	}

	covered := map[string][]SubtreeEntry{}
	out := make([]Action, 0, len(plan))
	for _, a := range plan {
		if a.Type == QuarantineLocal || a.Type == TrashRemote {
			if root, ok := rootFor(roots, a.RelPath); ok && kindOf[root] == a.Type {
				covered[root] = append(covered[root], SubtreeEntry{
					RelPath: a.RelPath, IsDir: a.IsDir, FileID: a.FileID,
					Size: a.LocalSize, MtimeNS: a.LocalMtimeNS,
				})
				continue
			}
		}
		out = append(out, a)
	}
	for i := range out {
		entries, ok := covered[out[i].RelPath]
		if !ok || !out[i].IsDir {
			continue
		}
		files := 0
		for _, e := range entries {
			if !e.IsDir {
				files++
			}
		}
		out[i].Subtree = entries
		out[i].SubtreeFiles = files
	}
	return out
}

// rootFor reports the collapsing directory that strictly contains p, if any.
// Roots never nest, so at most one can match.
func rootFor(roots []string, p string) (string, bool) {
	for _, r := range roots {
		if strings.HasPrefix(p, r+"/") {
			return r, true
		}
	}
	return "", false
}

// remoteFullyCovered reports whether every remote item under dir is one the
// plan already deletes. Trashing a folder on Drive takes its whole subtree
// with it — restorable, but still content the plan never reasoned about — so
// a survivor up there means the folder is trashed the long way, item by item,
// leaving the stranger alone (W14-M4).
func remoteFullyCovered(plan []Action, remote map[string]RemoteItem, dir string) bool {
	planned := map[string]bool{}
	for _, a := range plan {
		switch a.Type {
		case TrashRemote, Forget:
			planned[a.RelPath] = true
		}
	}
	prefix := dir + "/"
	for p := range remote {
		if strings.HasPrefix(p, prefix) && !planned[p] {
			return false
		}
	}
	return true
}

// deletesOnlyUnder reports whether every action touching a path strictly
// under dir is delete-class. A move counts as touching both of its ends: a
// file moved out of the subtree still has to be moved before the directory
// goes anywhere.
func deletesOnlyUnder(plan []Action, dir string) bool {
	prefix := dir + "/"
	for _, a := range plan {
		switch a.Type {
		case TrashRemote, QuarantineLocal, Forget:
			continue
		}
		if strings.HasPrefix(a.RelPath, prefix) ||
			(a.NewRelPath != "" && strings.HasPrefix(a.NewRelPath, prefix)) {
			return false
		}
	}
	return true
}
