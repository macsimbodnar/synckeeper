package engine

// S023: both machines rename one tracked file to the same new name. The
// baseline still says which side changed the content; a double rename is not
// by itself a conflict.

import (
	"context"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
)

// driveFileNamed is the id of the file named name directly under folder, "" if none.
// The tests compare it before and after: the rename keeps one Drive item, and
// the outcome must keep it too — no second item (W17).
func driveFileNamed(t *testing.T, fake *driveclient.Fake, folder, name string) string {
	t.Helper()
	kids, err := fake.List(context.Background(), folder)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range kids {
		if !k.IsDir() && k.Name == name {
			return k.ID
		}
	}
	return ""
}

func wantSameItem(t *testing.T, fake *driveclient.Fake, folder, name, id string) {
	t.Helper()
	if got := driveFileNamed(t, fake, folder, name); id == "" || got != id {
		t.Errorf("[drive] %s is item %q, want the renamed item %q", name, got, id)
	}
}

// The reviewer's shape (2026-11-11): a's upload went partial (S011 records
// Drive's bytes as a's own write), b renamed the file, a made the same rename
// before re-uploading. Was: final.mkv = the partial bytes everywhere and the
// complete file under the conflict name.
func TestS023DoubleRenameAfterPartialUploadKeepsCompleteFile(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)

	const part1, part2, part3 = "PART-1|", "PART-2|", "PART-3"
	a.write(t, "video.mkv", part1)
	grow := appender(t, a, "video.mkv")
	done := false
	a.eng.Client = &hookClient{Client: fake,
		onUpload: func(string) {
			if !done {
				grow(part2) // between the hash and the stream
			}
		},
		afterUpload: func(string) {
			if !done {
				done = true
				grow(part3) // after Drive stored the bytes
			}
		},
	}
	res, err := a.eng.Sync(context.Background(), Options{})
	if err != nil {
		t.Fatalf("a, cycle 1: %v", err)
	}
	if !done || res.Failed != 1 {
		t.Fatalf("a, cycle 1: want the upload to run and be reported failed (Drive holds a partial copy), got done=%v failed=%d %v",
			done, res.Failed, res.Errors)
	}
	a.eng.Client = fake

	b.sync(t) // b holds the partial bytes
	b.rename(t, "video.mkv", "final.mkv")
	b.sync(t)
	id := driveFileNamed(t, fake, root, "final.mkv")
	a.rename(t, "video.mkv", "final.mkv")

	for i := 0; i < 3; i++ {
		a.sync(t)
		b.sync(t)
	}
	const whole = part1 + part2 + part3
	wantOnly(t, "a", a.listTree(t), "final.mkv", whole)
	wantOnly(t, "b", b.listTree(t), "final.mkv", whole)
	wantOnly(t, "drive", driveContents(t, fake, root), "final.mkv", whole)
	wantSameItem(t, fake, root, "final.mkv", id)
}

// The same without S011: a renames and edits, b only renames.
func TestS023DoubleRenameLocalEditWins(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "video.mkv", "v1, synced everywhere")
	a.sync(t)
	b.sync(t)

	b.rename(t, "video.mkv", "final.mkv")
	b.sync(t)
	id := driveFileNamed(t, fake, root, "final.mkv")
	a.rename(t, "video.mkv", "final.mkv")
	a.write(t, "final.mkv", "v2, edited on a after its rename")

	for i := 0; i < 3; i++ {
		a.sync(t)
		b.sync(t)
	}
	const v2 = "v2, edited on a after its rename"
	wantOnly(t, "a", a.listTree(t), "final.mkv", v2)
	wantOnly(t, "b", b.listTree(t), "final.mkv", v2)
	wantOnly(t, "drive", driveContents(t, fake, root), "final.mkv", v2)
	wantSameItem(t, fake, root, "final.mkv", id)
}

// The mirror shape: b renames and edits, a only renames. b syncs the rename
// before the edit, so Drive keeps one item that moved and changed (a rename
// and edit in one cycle is delete + create on b, not this branch).
func TestS023DoubleRenameRemoteEditWins(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "video.mkv", "v1, synced everywhere")
	a.sync(t)
	b.sync(t)

	b.rename(t, "video.mkv", "final.mkv")
	b.sync(t)
	id := driveFileNamed(t, fake, root, "final.mkv")
	b.write(t, "final.mkv", "v2, edited on b after its rename")
	b.sync(t)
	a.rename(t, "video.mkv", "final.mkv")

	for i := 0; i < 3; i++ {
		a.sync(t)
		b.sync(t)
	}
	const v2 = "v2, edited on b after its rename"
	wantOnly(t, "a", a.listTree(t), "final.mkv", v2)
	wantOnly(t, "b", b.listTree(t), "final.mkv", v2)
	wantOnly(t, "drive", driveContents(t, fake, root), "final.mkv", v2)
	wantSameItem(t, fake, root, "final.mkv", id)
}

// Control: both machines rename AND edit to different content — a true
// conflict. Drive's version keeps the name, a's becomes the conflict copy,
// both on every side. b's rename and edit sync in two cycles, as above.
func TestS023DoubleRenameDoubleEditConflicts(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)
	a.write(t, "video.mkv", "v1, synced everywhere")
	a.sync(t)
	b.sync(t)

	const editA, editB = "edit made on a", "edit made on b, longer"
	b.rename(t, "video.mkv", "final.mkv")
	b.sync(t)
	id := driveFileNamed(t, fake, root, "final.mkv")
	b.write(t, "final.mkv", editB)
	b.sync(t)
	a.rename(t, "video.mkv", "final.mkv")
	a.write(t, "final.mkv", editA)

	for i := 0; i < 3; i++ {
		a.sync(t)
		b.sync(t)
	}
	for where, tree := range map[string]map[string]string{
		"a": a.listTree(t), "b": b.listTree(t), "drive": driveContents(t, fake, root),
	} {
		if len(tree) != 2 || tree["final.mkv"] != editB {
			t.Errorf("[%s] want final.mkv = %q and one conflict copy, have %v", where, editB, tree)
			continue
		}
		for name, content := range tree {
			if name != "final.mkv" && (!strings.HasPrefix(name, "final (conflict a ") || content != editA) {
				t.Errorf("[%s] want a's edit under a's conflict name, have %s = %q", where, name, content)
			}
		}
	}
	wantSameItem(t, fake, root, "final.mkv", id)
}
