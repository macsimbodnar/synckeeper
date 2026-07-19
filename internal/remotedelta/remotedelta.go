// Package remotedelta maintains the remote_nodes cache from Drive's changes
// feed and derives the remote snapshot for reconcile. The cache may contain
// nodes outside the synced tree; membership is resolved by walking parent
// links from the root folder when the snapshot is built.
package remotedelta

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/names"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// metaWalkDone marks that the initial full walk has been performed.
const metaWalkDone = "initial_walk_done"

// Refresh brings the remote_nodes cache up to date: a full walk on the
// first sync, then incremental changes, then a prune of unreachable rows.
// The page token is persisted only after its batch is fully applied
// (re-applying a batch is idempotent).
func Refresh(ctx context.Context, client driveclient.Client, db *statedb.DB, rootID string) error {
	if _, err := db.GetMeta(metaWalkDone); errors.Is(err, statedb.ErrNotFound) {
		if err := fullWalk(ctx, client, db, rootID); err != nil {
			return err
		}
		if err := db.SetMeta(metaWalkDone, "1"); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := consumeChanges(ctx, client, db, rootID); err != nil {
		return err
	}
	return prune(db, rootID)
}

// ForceFullWalk rebuilds the cache from scratch and resets the page token,
// used by `doctor --repair` when the DB (or its metadata) was lost. The
// token is fetched before walking so changes racing the walk are replayed
// by the next consume (idempotent upserts).
func ForceFullWalk(ctx context.Context, client driveclient.Client, db *statedb.DB, rootID string) error {
	token, err := client.StartPageToken(ctx)
	if err != nil {
		return err
	}
	if err := db.ClearRemoteNodes(); err != nil {
		return err
	}
	if err := fullWalk(ctx, client, db, rootID); err != nil {
		return err
	}
	if err := db.SetMeta(statedb.MetaPageToken, token); err != nil {
		return err
	}
	return db.SetMeta(metaWalkDone, "1")
}

func fullWalk(ctx context.Context, client driveclient.Client, db *statedb.DB, rootID string) error {
	queue := []string{rootID}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		children, err := client.List(ctx, parent)
		if err != nil {
			return fmt.Errorf("initial walk: %w", err)
		}
		for _, f := range children {
			if err := db.UpsertRemoteNode(toNode(f, parent)); err != nil {
				return err
			}
			if f.IsDir() {
				queue = append(queue, f.ID)
			}
		}
	}
	return nil
}

func consumeChanges(ctx context.Context, client driveclient.Client, db *statedb.DB, rootID string) error {
	token, err := db.GetMeta(statedb.MetaPageToken)
	if err != nil {
		return fmt.Errorf("no changes page token stored; run `synckeeper init`: %w", err)
	}
	for {
		page, err := client.Changes(ctx, token)
		if err != nil {
			return err
		}
		var walks []string
		for _, c := range page.Changes {
			switch {
			case c.Removed || c.File == nil || c.File.Trashed:
				// Trashed rows would only ever be filtered out again, so
				// they are dropped here rather than cached as tombstones.
				if err := db.DeleteRemoteNode(c.FileID); err != nil {
					return err
				}
			default:
				parent := ""
				if len(c.File.Parents) > 0 {
					parent = c.File.Parents[0]
				}
				// A folder we have never seen, attached to a known parent,
				// may carry a whole subtree for which no change events will
				// ever arrive (moved or restored into the tree): walk it.
				if c.File.IsDir() {
					known, err := db.HasRemoteNode(c.File.ID)
					if err != nil {
						return err
					}
					parentKnown := parent == rootID
					if !parentKnown && parent != "" {
						if parentKnown, err = db.HasRemoteNode(parent); err != nil {
							return err
						}
					}
					if !known && parentKnown {
						walks = append(walks, c.File.ID)
					}
				}
				if err := db.UpsertRemoteNode(toNode(*c.File, parent)); err != nil {
					return err
				}
			}
		}
		// Walk before the token is persisted: a failed walk is replayed
		// with its batch on the next refresh (upserts are idempotent).
		for _, id := range walks {
			if err := fullWalk(ctx, client, db, id); err != nil {
				return err
			}
		}
		switch {
		case page.NextPageToken != "":
			token = page.NextPageToken
		case page.NewStartToken != "":
			return db.SetMeta(statedb.MetaPageToken, page.NewStartToken)
		default:
			return errors.New("changes feed returned neither next page nor new start token")
		}
	}
}

// prune drops cache rows unreachable from the root folder: children of
// deleted folders, out-of-tree files the drive-wide changes feed dragged in,
// and legacy trashed tombstones. Without it the cache grows monotonically
// with all Drive activity, and every sync cycle loads the whole table. A
// pruned subtree that later re-enters the tree is restored by the
// unknown-folder walk in consumeChanges.
func prune(db *statedb.DB, rootID string) error {
	nodes, err := db.AllRemoteNodes()
	if err != nil {
		return err
	}
	children := map[string][]string{}
	for _, n := range nodes {
		if !n.Trashed {
			children[n.ParentID] = append(children[n.ParentID], n.FileID)
		}
	}
	reachable := map[string]bool{}
	queue := []string{rootID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, child := range children[id] {
			if !reachable[child] {
				reachable[child] = true
				queue = append(queue, child)
			}
		}
	}
	for _, n := range nodes {
		if !reachable[n.FileID] {
			if err := db.DeleteRemoteNode(n.FileID); err != nil {
				return err
			}
		}
	}
	return nil
}

// foldCollisionReason labels a fold collision by its cause: case alone,
// normalization alone, or both. It attributes to case when dropping the case
// fold would separate the names, to normalization when dropping the norm fold
// would, so the common single-cause collisions read precisely.
func foldCollisionReason(name, first string, caseFold, normFold bool) string {
	switch {
	case caseFold && names.FoldKey(name, false, normFold) != names.FoldKey(first, false, normFold):
		return fmt.Sprintf("case-collision with %q on a case-insensitive filesystem; not synced here", first)
	case normFold && names.FoldKey(name, caseFold, false) != names.FoldKey(first, caseFold, false):
		return fmt.Sprintf("normalization-collision with %q on a normalization-insensitive filesystem; not synced here", first)
	default:
		return fmt.Sprintf("collides with %q under this filesystem's name folding; not synced here", first)
	}
}

func toNode(f driveclient.File, parent string) statedb.RemoteNode {
	return statedb.RemoteNode{
		FileID:   f.ID,
		ParentID: parent,
		Name:     f.Name,
		MimeType: f.MimeType,
		MD5:      f.MD5,
		Size:     f.Size,
		Version:  f.Version,
		Trashed:  f.Trashed,
	}
}

// Snapshot derives the reconcile remote snapshot from the cache: BFS from
// rootID over non-trashed nodes, skipping Google-native files, ignored and
// invalid names, and deduplicating same-name siblings (first by id wins).
// When the local FS folds case (caseFold, e.g. APFS) and/or Unicode
// normalization (normFold), siblings that collide under that folding also
// collapse: the first by id is kept and the rest are skipped and reported, so
// a download can never silently clobber another.
func Snapshot(db *statedb.DB, rootID string, ignore []string, caseFold, normFold bool) (map[string]reconcile.RemoteItem, []reconcile.Skip, error) {
	nodes, err := db.AllRemoteNodes()
	if err != nil {
		return nil, nil, err
	}
	children := map[string][]statedb.RemoteNode{}
	for _, n := range nodes {
		if !n.Trashed {
			children[n.ParentID] = append(children[n.ParentID], n)
		}
	}

	snapshot := map[string]reconcile.RemoteItem{}
	var skips []reconcile.Skip
	type frame struct{ id, relPath string }
	// visited mirrors prune's reachable set (R17): a corrupted cache row
	// that makes a folder its own ancestor must not hang the daemon.
	visited := map[string]bool{rootID: true}
	queue := []frame{{id: rootID}}
	for len(queue) > 0 {
		f := queue[0]
		queue = queue[1:]
		kids := children[f.id]
		sort.Slice(kids, func(i, j int) bool { return kids[i].FileID < kids[j].FileID })
		seen := map[string]bool{}
		foldSeen := map[string]string{} // fold key -> first actual name kept
		for _, n := range kids {
			rel := names.Join(f.relPath, n.Name)
			switch {
			case strings.HasPrefix(n.MimeType, driveclient.GoogleAppsPrefix) && n.MimeType != driveclient.FolderMimeType:
				skips = append(skips, reconcile.Skip{RelPath: rel, Reason: "Google-native file; not synced"})
				continue
			case names.Ignored(n.Name, ignore):
				continue
			case names.Validate(n.Name) != nil:
				skips = append(skips, reconcile.Skip{RelPath: rel, Reason: "name not representable on disk"})
				continue
			case seen[n.Name]:
				skips = append(skips, reconcile.Skip{RelPath: rel, Reason: "duplicate name in Drive folder; kept first by id"})
				continue
			}
			if caseFold || normFold {
				key := names.FoldKey(n.Name, caseFold, normFold)
				if first, ok := foldSeen[key]; ok {
					skips = append(skips, reconcile.Skip{RelPath: rel,
						Reason: foldCollisionReason(n.Name, first, caseFold, normFold)})
					continue
				}
				foldSeen[key] = n.Name
			}
			seen[n.Name] = true
			isDir := n.MimeType == driveclient.FolderMimeType
			snapshot[rel] = reconcile.RemoteItem{
				FileID:  n.FileID,
				IsDir:   isDir,
				Size:    n.Size,
				MD5:     n.MD5,
				Version: n.Version,
			}
			if isDir && !visited[n.FileID] {
				visited[n.FileID] = true
				queue = append(queue, frame{id: n.FileID, relPath: rel})
			}
		}
	}
	return snapshot, skips, nil
}
