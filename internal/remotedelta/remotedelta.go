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
// first sync, then incremental changes. The page token is persisted only
// after its batch is fully applied (re-applying a batch is idempotent).
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
	return consumeChanges(ctx, client, db)
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

func consumeChanges(ctx context.Context, client driveclient.Client, db *statedb.DB) error {
	token, err := db.GetMeta(statedb.MetaPageToken)
	if err != nil {
		return fmt.Errorf("no changes page token stored; run `synckeeper init`: %w", err)
	}
	for {
		page, err := client.Changes(ctx, token)
		if err != nil {
			return err
		}
		for _, c := range page.Changes {
			switch {
			case c.Removed || c.File == nil:
				if err := db.DeleteRemoteNode(c.FileID); err != nil {
					return err
				}
			default:
				parent := ""
				if len(c.File.Parents) > 0 {
					parent = c.File.Parents[0]
				}
				if err := db.UpsertRemoteNode(toNode(*c.File, parent)); err != nil {
					return err
				}
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
func Snapshot(db *statedb.DB, rootID string, ignore []string) (map[string]reconcile.RemoteItem, []reconcile.Skip, error) {
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
	queue := []frame{{id: rootID}}
	for len(queue) > 0 {
		f := queue[0]
		queue = queue[1:]
		kids := children[f.id]
		sort.Slice(kids, func(i, j int) bool { return kids[i].FileID < kids[j].FileID })
		seen := map[string]bool{}
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
			seen[n.Name] = true
			isDir := n.MimeType == driveclient.FolderMimeType
			snapshot[rel] = reconcile.RemoteItem{
				FileID:  n.FileID,
				IsDir:   isDir,
				Size:    n.Size,
				MD5:     n.MD5,
				Version: n.Version,
			}
			if isDir {
				queue = append(queue, frame{id: n.FileID, relPath: rel})
			}
		}
	}
	return snapshot, skips, nil
}
