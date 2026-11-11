package executor

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
	"github.com/macsimbodnar/synckeeper/internal/trash"
)

// S013 (review): the local write gate's structural net. Whatever the planner
// gets wrong, no local mutation may write through a symlink inside the sync
// dir — into the folder it points at, outside the sync dir — or over one.
// Each action is applied on its own, against a tree where "photos" is a
// symlink to a folder outside, and every one of them would succeed without
// the net (the target files exist with the stats the actions pin).
func TestGateRefusesEveryLocalMutationThroughASymlink(t *testing.T) {
	ctx := context.Background()
	stat := func(t *testing.T, p string) os.FileInfo {
		t.Helper()
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	cases := []struct {
		name string
		act  func(outside string, remote driveclient.File) reconcile.Action
	}{
		{"mkdir_local under the link", func(string, driveclient.File) reconcile.Action {
			return reconcile.Action{Type: reconcile.MkdirLocal, RelPath: "photos/sub", FileID: "dir-id", IsDir: true}
		}},
		{"mkdir_local onto the link", func(string, driveclient.File) reconcile.Action {
			return reconcile.Action{Type: reconcile.MkdirLocal, RelPath: "photos", FileID: "dir-id", IsDir: true}
		}},
		{"download under the link", func(_ string, r driveclient.File) reconcile.Action {
			return reconcile.Action{Type: reconcile.Download, RelPath: "photos/new.jpg", FileID: r.ID, MD5: r.MD5, Size: r.Size}
		}},
		{"download replacing a file under the link", func(outside string, r driveclient.File) reconcile.Action {
			info := stat(t, filepath.Join(outside, "a.jpg"))
			return reconcile.Action{Type: reconcile.Download, RelPath: "photos/a.jpg", FileID: r.ID, MD5: r.MD5, Size: r.Size,
				LocalExists: true, LocalSize: info.Size(), LocalMtimeNS: info.ModTime().UnixNano()}
		}},
		{"move_local into the link", func(string, driveclient.File) reconcile.Action {
			return reconcile.Action{Type: reconcile.MoveLocal, RelPath: "keep.txt", NewRelPath: "photos/keep.txt"}
		}},
		{"move_local of the link itself", func(string, driveclient.File) reconcile.Action {
			return reconcile.Action{Type: reconcile.MoveLocal, RelPath: "photos", NewRelPath: "renamed", IsDir: true}
		}},
		{"conflict_backup under the link", func(string, driveclient.File) reconcile.Action {
			return reconcile.Action{Type: reconcile.ConflictBackup, RelPath: "photos/a.jpg", NewRelPath: "photos/a (conflict).jpg"}
		}},
		{"record under the link", func(outside string, r driveclient.File) reconcile.Action {
			info := stat(t, filepath.Join(outside, "a.jpg"))
			return reconcile.Action{Type: reconcile.Record, RelPath: "photos/a.jpg", FileID: r.ID, MD5: r.MD5, Size: r.Size,
				LocalExists: true, LocalSize: info.Size(), LocalMtimeNS: info.ModTime().UnixNano()}
		}},
		{"quarantine_local under the link", func(outside string, r driveclient.File) reconcile.Action {
			info := stat(t, filepath.Join(outside, "a.jpg"))
			return reconcile.Action{Type: reconcile.QuarantineLocal, RelPath: "photos/a.jpg", FileID: r.ID,
				LocalExists: true, LocalSize: info.Size(), LocalMtimeNS: info.ModTime().UnixNano()}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			syncDir := filepath.Join(base, "sync")
			outside := filepath.Join(base, "outside")
			for _, d := range []string{syncDir, outside} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(outside, "a.jpg"), []byte("mine"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(syncDir, "keep.txt"), []byte("k"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(syncDir, "photos")); err != nil {
				t.Skipf("cannot create symlinks here: %v", err)
			}
			db, err := statedb.Open(filepath.Join(base, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			fake := driveclient.NewFake()
			folder, err := fake.Mkdir(ctx, driveclient.FakeRootID, "Synckeeper")
			if err != nil {
				t.Fatal(err)
			}
			remote, err := fake.Upload(ctx, folder.ID, "x.jpg", strings.NewReader("from drive"), 10)
			if err != nil {
				t.Fatal(err)
			}
			x := &Executor{DB: db, Client: fake, SyncDir: syncDir,
				QuarantineDir: filepath.Join(base, "quarantine"), RootID: folder.ID,
				Trash: trash.NewFake(filepath.Join(base, "bin"))}
			x.pathIDs = map[string]string{"": folder.ID, "photos": "dir-id"}

			before := listDir(t, outside)
			sum, err := x.Apply(ctx, []reconcile.Action{tc.act(outside, remote)})
			if err != nil {
				t.Fatal(err)
			}
			if sum.Failed != 1 || !strings.Contains(strings.Join(sum.Errors, " "), "is a symlink") {
				t.Errorf("executed/failed = %d/%d, want the action refused by the symlink check: %v", sum.Executed, sum.Failed, sum.Errors)
			}
			if after := listDir(t, outside); strings.Join(after, ",") != strings.Join(before, ",") {
				t.Errorf("the folder behind the link changed: %v → %v", before, after)
			}
			if info, err := os.Lstat(filepath.Join(syncDir, "photos")); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Errorf("the link itself was moved or replaced (err %v)", err)
			}
		})
	}
}

// The root itself may be a symlink (S007): the net starts below it.
func TestGateAllowsASymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	x := &Executor{SyncDir: link}
	if err := x.refuseLinkedPaths(reconcile.Action{Type: reconcile.MkdirLocal, RelPath: "a/b"}); err != nil {
		t.Fatalf("a symlinked root was refused: %v", err)
	}
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		info, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, rel+"|"+info.ModTime().String())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}
