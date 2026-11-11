package engine

// S011 (audit 2026-11-11 F05): a file still being written while it uploads.
// The create arm is TestAudit20261111_F05_FileWrittenDuringUploadIsTruncatedAtCanonicalName;
// these cover the update arm, a writer that never pauses, and the crash
// window the fix leaves open.

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/executor"
)

// appender appends to one file of a machine, standing in for a program that
// is still writing it.
func appender(t *testing.T, m *machine, rel string) func(string) {
	return func(s string) {
		f, err := os.OpenFile(filepath.Join(m.dir, filepath.FromSlash(rel)), os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Errorf("append: %v", err)
			return
		}
		f.WriteString(s)
		f.Close()
	}
}

// driveContents returns name -> content for the files directly under folder.
func driveContents(t *testing.T, fake *driveclient.Fake, folder string) map[string]string {
	t.Helper()
	kids, err := fake.List(context.Background(), folder)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, k := range kids {
		if k.IsDir() {
			continue
		}
		if _, dup := out[k.Name]; dup {
			t.Errorf("two items named %s on Drive", k.Name)
		}
		body, err := fake.Download(context.Background(), k.ID)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(body)
		body.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[k.Name] = string(raw)
	}
	return out
}

func wantOnly(t *testing.T, where string, tree map[string]string, rel, content string) {
	t.Helper()
	if len(tree) != 1 || tree[rel] != content {
		t.Errorf("[%s] want exactly %s = %q, have %d file(s): %v", where, rel, content, len(tree), tree)
	}
}

// The UpdateRemote arm: an existing, synced file is rewritten in place (an
// export overwriting its previous output) and the cycle uploads it before the
// writer is done. Drive's file then holds this machine's own partial write
// under the existing id; the next cycle must upload the finished file over it,
// not read it as another machine's edit and conflict.
func TestS011UpdateOfFileStillBeingWrittenKeepsCompleteFileAtCanonicalName(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)

	a.write(t, "video.mkv", "the previous export, synced everywhere")
	a.sync(t)
	b.sync(t)

	const part1, part2, part3 = "PART-1|", "PART-2|", "PART-3"
	a.write(t, "video.mkv", part1) // the rewrite has started when the cycle scans
	grow := appender(t, a, "video.mkv")
	done := false
	a.eng.Client = &hookClient{Client: fake,
		onUpdate: func(string) {
			if !done {
				grow(part2) // between the hash and the stream
			}
		},
		afterUpdate: func(string) {
			if !done {
				done = true
				grow(part3) // after Drive stored the revision
			}
		},
	}
	res, err := a.eng.Sync(context.Background(), Options{})
	if err != nil {
		t.Fatalf("cycle 1: %v", err)
	}
	if !done || res.Failed != 1 {
		t.Fatalf("cycle 1: want the one update to run and be reported failed (Drive holds a partial copy), got done=%v failed=%d %v",
			done, res.Failed, res.Errors)
	}
	a.eng.Client = fake

	for i := 0; i < 3; i++ {
		a.sync(t)
		b.sync(t)
	}

	const whole = part1 + part2 + part3
	wantOnly(t, "a", a.listTree(t), "video.mkv", whole)
	wantOnly(t, "b", b.listTree(t), "video.mkv", whole)
	wantOnly(t, "drive", driveContents(t, fake, root), "video.mkv", whole)
}

// A file written without pause (a log being appended) is mid-write at every
// upload. Each cycle must land a newer snapshot on Drive under the one item —
// never starved, never a conflict copy, never a second item — and the file
// converges once the writer stops.
func TestS011FileWrittenAcrossCyclesIsNeverStarvedNorConflicted(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	b := newMachine(t, "b", fake, root)

	a.write(t, "app.log", "start\n")
	grow := appender(t, a, "app.log")
	line := 0
	before := func() { line++; grow(strings.Repeat("x", line) + " before\n") }
	after := func() { grow(strings.Repeat("y", line) + " after\n") }
	a.eng.Client = &hookClient{Client: fake,
		onUpload:    func(string) { before() },
		afterUpload: func(string) { after() },
		onUpdate:    func(string) { before() },
		afterUpdate: func(string) { after() },
	}

	prev := 0
	for cycle := 1; cycle <= 5; cycle++ {
		res, err := a.eng.Sync(context.Background(), Options{})
		if err != nil {
			t.Fatalf("cycle %d: %v", cycle, err)
		}
		if res.Failed != 1 {
			t.Fatalf("cycle %d: want the one upload reported failed (still being written), got %d: %v", cycle, res.Failed, res.Errors)
		}
		b.sync(t)

		onDrive := driveContents(t, fake, root)
		got, ok := onDrive["app.log"]
		if len(onDrive) != 1 || !ok {
			t.Fatalf("cycle %d: want one app.log on Drive, have %v", cycle, onDrive)
		}
		if len(got) <= prev {
			t.Fatalf("cycle %d: Drive's snapshot did not advance (%d bytes, was %d): the writer starves the upload", cycle, len(got), prev)
		}
		prev = len(got)
		if !strings.HasPrefix(a.read(t, "app.log"), got) {
			t.Fatalf("cycle %d: Drive holds %q, not a snapshot of the file being written", cycle, got)
		}
		wantOnly(t, "b", b.listTree(t), "app.log", got)
		if tree := a.listTree(t); len(tree) != 1 {
			t.Fatalf("cycle %d: a has %v, want app.log alone", cycle, tree)
		}
	}

	a.eng.Client = fake // the writer stops
	a.sync(t)
	b.sync(t)
	final := a.read(t, "app.log")
	wantOnly(t, "a", a.listTree(t), "app.log", final)
	wantOnly(t, "b", b.listTree(t), "app.log", final)
	wantOnly(t, "drive", driveContents(t, fake, root), "app.log", final)
	if res := a.sync(t); len(res.Plan) != 0 {
		t.Errorf("not at rest after the writer stopped: %d actions planned", len(res.Plan))
	}
}

// The window S011 leaves open: the process dies after Drive stored the
// partial upload and before its row commits (one local transaction). Nothing
// then says the bytes on Drive are this machine's own, so §4.2 meets them as
// another machine's — the pre-S011 outcome: the partial copy keeps the
// canonical name and the complete file becomes the conflict copy. Nothing is
// lost. Closing it needs Drive-side proof of authorship (a tag written with
// the upload) or the created id in the journal (W17's not-taken option) —
// owner decisions. Unskip with that fix; it is the red test.
func TestS011CrashAfterPartialUploadLandsResidual(t *testing.T) {
	t.Skip("known residual of S011: crash between a partial upload landing and its commit; see the comment")
	for _, shape := range []string{"create", "update"} {
		t.Run(shape, func(t *testing.T) {
			fake, root := newWorld(t)
			a := newMachine(t, "a", fake, root)
			b := newMachine(t, "b", fake, root)
			if shape == "update" {
				a.write(t, "video.mkv", "the previous export, synced everywhere")
				a.sync(t)
				b.sync(t)
			}
			const part1, part2, part3 = "PART-1|", "PART-2|", "PART-3"
			a.write(t, "video.mkv", part1)
			grow := appender(t, a, "video.mkv")
			done := false
			beforeStream := func() {
				if !done {
					grow(part2)
				}
			}
			afterStream := func() {
				if !done {
					done = true
					grow(part3)
				}
			}
			a.eng.Client = &hookClient{Client: fake,
				onUpload: func(string) { beforeStream() }, afterUpload: func(string) { afterStream() },
				onUpdate: func(string) { beforeStream() }, afterUpdate: func(string) { afterStream() },
			}
			arm(t, executor.CPUploadBeforeCommit)
			syncExpectFailure(t, a)
			disarm()
			a.eng.Client = fake

			for i := 0; i < 3; i++ {
				a.sync(t)
				b.sync(t)
			}
			const whole = part1 + part2 + part3
			wantOnly(t, "a", a.listTree(t), "video.mkv", whole)
			wantOnly(t, "b", b.listTree(t), "video.mkv", whole)
		})
	}
}
