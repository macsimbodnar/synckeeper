package watch

// W13-T5: what the user reads in `activity` after a folder is deleted in
// Drive — one line for the folder with its file count, naming the bin it
// went to (or the quarantine, on a platform that has none).

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/engine"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

func recordedActivity(t *testing.T, res *engine.Result) []statedb.Activity {
	t.Helper()
	db, err := statedb.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	newRecorder(db).recordActivity(res)
	acts, err := db.RecentActivity(50)
	if err != nil {
		t.Fatal(err)
	}
	return acts
}

func TestActivityReportsOneTrashEntryPerSubtree(t *testing.T) {
	res := &engine.Result{
		TrashAvailable: true,
		Plan: []reconcile.Action{{
			Type: reconcile.QuarantineLocal, RelPath: "pack", IsDir: true, SubtreeFiles: 1145,
		}},
	}
	acts := recordedActivity(t, res)
	if len(acts) != 1 {
		t.Fatalf("recorded %d entries, want one line for the whole folder: %+v", len(acts), acts)
	}
	a := acts[0]
	if a.Kind != "trash" || a.Source != "remote" || a.RelPath != "pack" {
		t.Errorf("entry = %+v, want a drive→local trash of pack", a)
	}
	if a.Detail != "(1145 files)" {
		t.Errorf("detail = %q, want the file count the one line stands for", a.Detail)
	}
}

// A tripped mass-delete guard strips the delete-class actions before
// anything runs (spec §8.1). They were planned, never performed — reporting
// them said "trashed 1117 files" once per cycle while the folder was still
// sitting in the sync dir, which is exactly the opposite of what happened.
// Found in the field 2026-07-26 (a folder deleted in Drive, held by the
// guard, reported as trashed every 45s).
func TestActivityNeverClaimsGuardBlockedDeletes(t *testing.T) {
	res := &engine.Result{
		TrashAvailable: true,
		GuardBlocked:   true,
		GuardReason:    "plan deletes 1117 of 1118 tracked files",
		Plan: []reconcile.Action{
			{Type: reconcile.QuarantineLocal, RelPath: "pack", IsDir: true, SubtreeFiles: 1117},
			{Type: reconcile.TrashRemote, RelPath: "gone.txt"},
			{Type: reconcile.Download, RelPath: "other.txt"}, // not delete-class: it did run
		},
	}
	acts := recordedActivity(t, res)
	for _, a := range acts {
		if a.Kind == "trash" {
			t.Errorf("reported %+v — the guard stripped that deletion; nothing was trashed", a)
		}
	}
	if len(acts) != 1 || acts[0].Kind != "download" {
		t.Errorf("entries = %+v, want only the action that actually ran", acts)
	}
}

// W14-M3: a large deletion is no longer a question, so it has to be a
// statement — the user must be able to find out inside the window their bin
// keeps things for.
func TestActivityReportsALargeDeletion(t *testing.T) {
	acts := recordedActivity(t, &engine.Result{
		TrashAvailable: true, LargeDeletion: true, DeletedLocal: 1117,
		Plan: []reconcile.Action{{Type: reconcile.QuarantineLocal, RelPath: "pack", IsDir: true, SubtreeFiles: 1117}},
	})
	var summary *statedb.Activity
	for i := range acts {
		if acts[i].Kind == "deleted" {
			summary = &acts[i]
		}
	}
	if summary == nil {
		t.Fatalf("no deletion summary recorded: %+v", acts)
	}
	if !strings.Contains(summary.Detail, "1117") || !strings.Contains(summary.Detail, "system bin") {
		t.Errorf("detail = %q, want the count and where to restore from", summary.Detail)
	}

	// The Drive side reads the other way round.
	acts = recordedActivity(t, &engine.Result{
		TrashAvailable: true, LargeDeletion: true, DeletedRemote: 900,
		Plan: []reconcile.Action{{Type: reconcile.TrashRemote, RelPath: "pack", IsDir: true, SubtreeFiles: 900}},
	})
	for _, a := range acts {
		if a.Kind == "deleted" && (!strings.Contains(a.Detail, "900") || !strings.Contains(a.Detail, "Drive")) {
			t.Errorf("detail = %q, want the count and Drive's bin", a.Detail)
		}
	}
}

// W14-M2: a bin that refused an item is invisible to a capability probe, so
// the cycle that fell back to the quarantine says so itself.
func TestActivityReportsQuarantineFallbacks(t *testing.T) {
	acts := recordedActivity(t, &engine.Result{
		TrashAvailable: true, QuarantineFell: 3,
		Plan: []reconcile.Action{{Type: reconcile.QuarantineLocal, RelPath: "a.txt"}},
	})
	found := false
	for _, a := range acts {
		if a.Kind == "quarantine" && strings.Contains(a.Detail, "3 items") {
			found = true
		}
	}
	if !found {
		t.Errorf("no fallback report recorded: %+v", acts)
	}
}

// Without a bin the same deletion is a quarantine, and says so: the report
// names the destination that was actually used.
func TestActivityNamesTheQuarantineWhenThereIsNoBin(t *testing.T) {
	res := &engine.Result{
		Plan: []reconcile.Action{{Type: reconcile.QuarantineLocal, RelPath: "gone.txt"}},
	}
	acts := recordedActivity(t, res)
	if len(acts) != 1 || acts[0].Kind != "quarantine" {
		t.Fatalf("entries = %+v, want one quarantine line", acts)
	}
}
