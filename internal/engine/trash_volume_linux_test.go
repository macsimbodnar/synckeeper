//go:build linux

package engine

// S016 (audit 2026-11-11 F11) on real hardware: a sync dir on another
// filesystem trashes into that volume's <mount>/.Trash-$uid, not the home
// trash. Where the mount root is not writable that bin cannot exist, every
// delete falls to the quarantine, and the mass-delete guard must therefore
// hold — it must not ask the home trash instead.
//
// Needs a prepared directory, so it is skipped unless one is named:
//
//	sudo mkdir -p /mnt/sk_f11 && sudo mount -t tmpfs tmpfs /mnt/sk_f11
//	sudo mkdir /mnt/sk_f11/sync && sudo chown $USER /mnt/sk_f11/sync
//	SYNCKEEPER_F11_DIR=/mnt/sk_f11/sync go test ./internal/engine -run TestF11 -v
//	sudo umount /mnt/sk_f11
//
// The same layout without sudo (a user namespace, capabilities dropped so the
// read-only mount root really refuses): adocs/testing.md S016.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/trash"
)

func TestF11GuardHoldsWhenTheSyncDirsVolumeTrashIsUnusable(t *testing.T) {
	prepared := os.Getenv("SYNCKEEPER_F11_DIR")
	if prepared == "" {
		t.Skip("set SYNCKEEPER_F11_DIR to a writable dir on a filesystem whose mount root you cannot write")
	}
	// The home trash is a temp dir: no test may touch the developer's own bin.
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir, err := os.MkdirTemp(prepared, "f11-")
	if err != nil {
		t.Fatalf("prepared dir is not writable: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if dev(t, dir) == dev(t, os.Getenv("XDG_DATA_HOME")) {
		t.Fatalf("%s is on the home trash's filesystem; the layout needs another one", prepared)
	}
	volTrash := filepath.Join(mountRoot(t, dir), fmt.Sprintf(".Trash-%d", os.Getuid()))
	if err := os.Mkdir(volTrash, 0o700); err == nil {
		os.Remove(volTrash)
		t.Fatalf("%s could be created; the layout needs a mount root you cannot write", volTrash)
	}

	fake, rootID := newWorld(t)
	m := newMachine(t, "A", fake, rootID)
	m.dir, m.eng.SyncDir = dir, dir
	m.eng.Trash = trash.OS() // the production Linux bin, not the suite's fake

	const files = 40 // over the floor of 10 and the 25% threshold
	for i := 0; i < files; i++ {
		m.write(t, fmt.Sprintf("pack/f%03d.txt", i), fmt.Sprintf("c-%d", i))
	}
	m.write(t, "keep.txt", "unrelated")
	m.sync(t)
	if err := fake.Trash(context.Background(), remoteChildID(t, fake, rootID, "pack")); err != nil {
		t.Fatal(err)
	}

	res, err := m.eng.Sync(context.Background(), Options{DeferMassDelete: true})
	if err != nil {
		t.Fatalf("daemon cycle must not fail on a guard trip: %v", err)
	}
	t.Logf("TrashAvailable=%v GuardBlocked=%v executed=%d QuarantineFell=%d",
		res.TrashAvailable, res.GuardBlocked, res.Executed, res.QuarantineFell)
	assertGuardHeld(t, res, reconcile.QuarantineLocal)
	if got := len(m.listTree(t)); got != files+1 {
		t.Errorf("guard blocked but %d of %d local files were removed anyway", files+1-got, files)
	}
}

func dev(t *testing.T, path string) uint64 {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		t.Fatal(err)
	}
	return uint64(st.Dev)
}

// mountRoot walks up from path while the device id stays the same.
func mountRoot(t *testing.T, path string) string {
	t.Helper()
	d := dev(t, path)
	for {
		parent := filepath.Dir(path)
		if parent == path || dev(t, parent) != d {
			return path
		}
		path = parent
	}
}
