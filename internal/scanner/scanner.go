// Package scanner walks the local sync dir and produces the local snapshot
// for reconcile. md5 is computed only when size or mtime differ from the
// baseline; otherwise the baseline hash is trusted.
package scanner

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/macsimbodnar/synckeeper/internal/names"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// Scan walks root and returns the local snapshot plus reported skips.
func Scan(root string, base map[string]reconcile.BaseItem, ignore []string) (map[string]reconcile.LocalItem, []reconcile.Skip, error) {
	snapshot := map[string]reconcile.LocalItem{}
	var skips []reconcile.Skip

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// One unreadable directory used to fail the whole cycle, every
			// cycle, visible only in the log (W18-G, review F4). It is now
			// reported and walked around — but the engine MUST hold the
			// baseline rows under it harmless, or "absent from the local
			// scan" reads as "deleted locally" and trashes them on Drive,
			// which is worse than the wedge. Skip.Unreadable is what tells it.
			//
			// Only a directory is tolerated: an unreadable root is already a
			// hard error before the scan starts (guards.EnsureSyncDir), and a
			// file that cannot be statted inside a readable directory means
			// the tree is moving under us — the cycle should fail and replan.
			if p == root || d == nil || !d.IsDir() {
				return err
			}
			rel, relErr := filepath.Rel(root, p)
			if relErr != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			skips = append(skips, reconcile.Skip{RelPath: rel, Unreadable: true,
				Reason: "directory could not be read; its contents are left untouched on both sides: " + err.Error()})
			// The folder itself is known to exist even though its contents are
			// not: say so, so its own baseline row is never read as deleted.
			snapshot[rel] = reconcile.LocalItem{IsDir: true}
			return nil
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		name := d.Name()

		if names.Ignored(name, ignore) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			skips = append(skips, reconcile.Skip{RelPath: rel, Reason: "symlink; not followed"})
			return nil
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			skips = append(skips, reconcile.Skip{RelPath: rel, Reason: "not a regular file"})
			return nil
		}
		if err := names.Validate(name); err != nil {
			skips = append(skips, reconcile.Skip{RelPath: rel, Reason: err.Error()})
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			snapshot[rel] = reconcile.LocalItem{IsDir: true}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		item := reconcile.LocalItem{
			Size:    info.Size(),
			MtimeNS: info.ModTime().UnixNano(),
		}
		if b, ok := base[rel]; ok && !b.IsDir && b.Size == item.Size && b.MtimeNS == item.MtimeNS {
			item.MD5 = b.MD5 // unchanged by stat; trust the baseline hash
		} else {
			sum, err := hashFile(p)
			if err != nil {
				return fmt.Errorf("hash %s: %w", rel, err)
			}
			item.MD5 = sum
		}
		snapshot[rel] = item
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return snapshot, skips, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
