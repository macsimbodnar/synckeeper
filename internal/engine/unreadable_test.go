package engine

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// W18.13 — the review's F4 reproduction, end to end: `chmod 000` a subtree,
// and the machine keeps syncing everything else while the Drive copies of what
// it can no longer see are left completely alone.
//
// Both halves matter and the second is the dangerous one. Before W18-G the
// scan failed outright, so the daemon stopped syncing entirely — bad. Making
// the scan tolerant WITHOUT holding the hidden baseline rows harmless would be
// worse: every file under the folder reads as "deleted locally" and its Drive
// copy goes to the bin.
func TestUnreadableSubtreeNeitherWedgesTheCycleNorTouchesDrive(t *testing.T) {
	fake, root := newWorld(t)
	m := newMachine(t, "a", fake, root)

	m.write(t, "keep.txt", "top level")
	m.write(t, "locked/a.txt", "inside")
	m.write(t, "locked/deep/b.txt", "deeper")
	m.sync(t)
	if driveBefore := driveTreeOf(t, fake, root); len(driveBefore) != 3 {
		t.Fatalf("setup: Drive should hold three files, got %v", sortedKeys(driveBefore))
	}

	locked := filepath.Join(m.dir, "locked")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Skipf("cannot chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	if _, err := os.ReadDir(locked); err == nil {
		t.Skip("this environment can read a 0000 directory (running as root?)")
	}

	// A change outside the locked folder, to prove the machine still syncs.
	m.write(t, "new.txt", "written while locked")

	res := m.sync(t) // would have hard-errored before W18-G
	if n := countDeleteClass(res.Plan); n != 0 {
		t.Fatalf("the cycle planned %d delete-class actions for content it simply could not see: %+v", n, res.Plan)
	}
	if driveNow := driveTreeOf(t, fake, root); len(driveNow) != 4 {
		t.Fatalf("Drive holds %v, want the original three plus new.txt", driveNow)
	}
	if got := driveTreeOf(t, fake, root)["locked/a.txt"]; got != "inside" {
		t.Errorf("locked/a.txt on Drive = %q, want it untouched", got)
	}

	// The user is told, rather than left to notice the folder stopped syncing.
	var reported bool
	for _, s := range res.Skips {
		if s.RelPath == "locked" && s.Unreadable {
			reported = true
		}
	}
	if !reported {
		t.Errorf("the unreadable folder was not reported as a skip: %+v", res.Skips)
	}

	// And it recovers by itself once the permission comes back: no repair
	// command, no re-init.
	if err := os.Chmod(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	m.write(t, "locked/c.txt", "after unlocking")
	m.sync(t)
	after := driveTreeOf(t, fake, root)
	if len(after) != 5 || after["locked/c.txt"] != "after unlocking" {
		t.Fatalf("after unlocking, Drive holds %v, want all five files", sortedKeys(after))
	}
}

// The daemon has no channel for skips yet (plan.md W10), so an unreadable
// folder would otherwise go from "wedges loudly" to "stops syncing silently".
// It warns once per folder, and re-arms so a recurrence is still reported —
// a poll every 45 s must not bury the log in identical lines.
func TestUnreadableFolderIsWarnedAboutOncePerOccurrence(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(old)

	e := &Engine{}
	locked := []reconcile.Skip{{RelPath: "locked", Unreadable: true, Reason: "permission denied"}}
	readable := []reconcile.Skip{{RelPath: "other", Reason: "symlink; not followed"}}

	e.warnUnreadable(locked)
	e.warnUnreadable(locked) // the same standing condition, next cycle
	if got := strings.Count(logs.String(), "cannot be read"); got != 1 {
		t.Fatalf("warned %d times about one standing condition, want 1:\n%s", got, logs.String())
	}
	e.warnUnreadable(readable) // permissions fixed
	e.warnUnreadable(locked)   // and broken again
	if got := strings.Count(logs.String(), "cannot be read"); got != 2 {
		t.Fatalf("warned %d times, want 2 — the warning must re-arm:\n%s", got, logs.String())
	}
}

// driveTreeOf reconstructs the non-trashed files under a Drive folder as
// rel_path → content.
func driveTreeOf(t *testing.T, fake *driveclient.Fake, rootID string) map[string]string {
	t.Helper()
	out := map[string]string{}
	ctx := context.Background()
	var walk func(parentID, prefix string)
	walk = func(parentID, prefix string) {
		children, err := fake.List(ctx, parentID)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range children {
			rel := c.Name
			if prefix != "" {
				rel = prefix + "/" + c.Name
			}
			if c.IsDir() {
				walk(c.ID, rel)
				continue
			}
			rc, err := fake.Download(ctx, c.ID)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				t.Fatal(err)
			}
			out[rel] = string(data)
		}
	}
	walk(rootID, "")
	return out
}

func countDeleteClass(plan []reconcile.Action) int {
	n := 0
	for _, a := range plan {
		if a.Type == reconcile.TrashRemote || a.Type == reconcile.QuarantineLocal {
			n++
		}
	}
	return n
}
