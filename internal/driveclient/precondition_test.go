package driveclient

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// S010: the remote write gate's verdicts, one per kind of drift, on every
// mutation it guards. The fake applies the same Precondition.verify the real
// client does, under its lock.

type gateWorld struct {
	fake         *Fake
	dir, other   File
	file, folder File
}

func newGateWorld(t *testing.T) gateWorld {
	t.Helper()
	ctx := context.Background()
	w := gateWorld{fake: NewFake()}
	var err error
	if w.dir, err = w.fake.Mkdir(ctx, FakeRootID, "sync"); err != nil {
		t.Fatal(err)
	}
	if w.other, err = w.fake.Mkdir(ctx, FakeRootID, "elsewhere"); err != nil {
		t.Fatal(err)
	}
	if w.file, err = w.fake.Upload(ctx, w.dir.ID, "notes.txt", strings.NewReader("v1"), 2); err != nil {
		t.Fatal(err)
	}
	if w.folder, err = w.fake.Mkdir(ctx, w.dir.ID, "docs"); err != nil {
		t.Fatal(err)
	}
	return w
}

func pinOf(f File, parent string) Precondition {
	return Precondition{FileID: f.ID, ParentID: parent, Name: f.Name, IsDir: f.IsDir(), MD5: f.MD5}
}

// mutate runs one gated mutation of id and reports its error.
func mutate(ctx context.Context, f *Fake, op, id, newParent string) error {
	var err error
	switch op {
	case "update":
		_, err = f.Update(ctx, id, strings.NewReader("mine"), 4)
	case "trash":
		err = f.Trash(ctx, id)
	case "move":
		_, err = f.Move(ctx, id, newParent, "moved")
	}
	return err
}

func TestPreconditionRefusesDrift(t *testing.T) {
	type drift struct {
		name   string
		folder bool // the target is the folder rather than the file
		apply  func(t *testing.T, w gateWorld, id string)
	}
	bg := context.Background()
	drifts := []drift{
		{"file content edited", false, func(t *testing.T, w gateWorld, id string) {
			if _, err := w.fake.Update(bg, id, strings.NewReader("theirs"), 6); err != nil {
				t.Fatal(err)
			}
		}},
		{"file renamed", false, func(t *testing.T, w gateWorld, id string) {
			if _, err := w.fake.Move(bg, id, w.dir.ID, "renamed.txt"); err != nil {
				t.Fatal(err)
			}
		}},
		{"file moved to another folder", false, func(t *testing.T, w gateWorld, id string) {
			if _, err := w.fake.Move(bg, id, w.other.ID, "notes.txt"); err != nil {
				t.Fatal(err)
			}
		}},
		{"file trashed", false, func(t *testing.T, w gateWorld, id string) {
			if err := w.fake.Trash(bg, id); err != nil {
				t.Fatal(err)
			}
		}},
		{"file gone", false, func(t *testing.T, w gateWorld, id string) { w.fake.Forget(id) }},
		{"folder renamed", true, func(t *testing.T, w gateWorld, id string) {
			if _, err := w.fake.Move(bg, id, w.dir.ID, "papers"); err != nil {
				t.Fatal(err)
			}
		}},
		{"folder moved to another folder", true, func(t *testing.T, w gateWorld, id string) {
			if _, err := w.fake.Move(bg, id, w.other.ID, "docs"); err != nil {
				t.Fatal(err)
			}
		}},
		{"folder trashed", true, func(t *testing.T, w gateWorld, id string) {
			if err := w.fake.Trash(bg, id); err != nil {
				t.Fatal(err)
			}
		}},
		{"folder gone", true, func(t *testing.T, w gateWorld, id string) { w.fake.Forget(id) }},
	}
	for _, d := range drifts {
		for _, op := range []string{"update", "trash", "move"} {
			if d.folder && op == "update" {
				continue // a folder has no content to update
			}
			t.Run(d.name+"/"+op, func(t *testing.T) {
				w := newGateWorld(t)
				target := w.file
				if d.folder {
					target = w.folder
				}
				ctx := WithPrecondition(bg, pinOf(target, w.dir.ID))
				d.apply(t, w, target.ID)
				before, beforeErr := w.fake.Get(bg, target.ID)
				trashes := w.fake.TrashCount()

				err := mutate(ctx, w.fake, op, target.ID, w.other.ID)
				if !errors.Is(err, ErrDrift) {
					t.Fatalf("%s after %q: err = %v, want ErrDrift", op, d.name, err)
				}
				after, afterErr := w.fake.Get(bg, target.ID)
				if (beforeErr == nil) != (afterErr == nil) || before.MD5 != after.MD5 || before.Name != after.Name ||
					before.Trashed != after.Trashed || before.Version != after.Version {
					t.Errorf("refused %s still changed Drive: before %+v, after %+v", op, before, after)
				}
				if w.fake.TrashCount() != trashes {
					t.Errorf("refused %s still sent a trash", op)
				}
			})
		}
	}
}

// What Drive holds matches the plan: every mutation goes through. A folder's
// pin ignores content entirely — a folder has none — so a child edited inside
// it does not refuse the folder's own mutation (the residual §7 names).
func TestPreconditionPassesWhenDriveMatches(t *testing.T) {
	bg := context.Background()
	for _, op := range []string{"update", "trash", "move"} {
		t.Run("file/"+op, func(t *testing.T) {
			w := newGateWorld(t)
			if err := mutate(WithPrecondition(bg, pinOf(w.file, w.dir.ID)), w.fake, op, w.file.ID, w.other.ID); err != nil {
				t.Fatalf("%s on an unchanged file: %v", op, err)
			}
		})
	}
	for _, op := range []string{"trash", "move"} {
		t.Run("folder/"+op, func(t *testing.T) {
			w := newGateWorld(t)
			if _, err := w.fake.Upload(bg, w.folder.ID, "inside.txt", strings.NewReader("x"), 1); err != nil {
				t.Fatal(err)
			}
			if err := mutate(WithPrecondition(bg, pinOf(w.folder, w.dir.ID)), w.fake, op, w.folder.ID, w.other.ID); err != nil {
				t.Fatalf("%s on an unchanged folder: %v", op, err)
			}
		})
	}
}

// A precondition naming another item must refuse, never be skipped: a gate
// that passes by accident is no gate.
func TestPreconditionForAnotherItemRefuses(t *testing.T) {
	w := newGateWorld(t)
	ctx := WithPrecondition(context.Background(), pinOf(w.folder, w.dir.ID))
	for _, op := range []string{"update", "trash", "move"} {
		if err := mutate(ctx, w.fake, op, w.file.ID, w.other.ID); err == nil {
			t.Errorf("%s of %s with %s's precondition went through", op, w.file.ID, w.folder.ID)
		}
	}
}
