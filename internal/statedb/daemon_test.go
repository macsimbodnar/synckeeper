package statedb

import (
	"fmt"
	"path/filepath"
	"testing"
)

func openTemp(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestDaemonStatusRoundTrip(t *testing.T) {
	db := openTemp(t)

	if _, err := db.GetDaemonStatus(); err != ErrNotFound {
		t.Fatalf("fresh DB: want ErrNotFound, got %v", err)
	}

	in := DaemonStatus{
		Running: true, PID: 4321, StartedAt: 1000, LastHeartbeatAt: 2000,
		Mode: "watching", Paused: false, LastSyncAt: 1900,
		LastCycleJSON: `{"actions":3,"executed":3,"failed":0,"duration_ms":42}`,
		NextPollAt:    2045, GuardBlocked: true, GuardReason: "needs --confirm-deletes",
	}
	if err := db.SetDaemonStatus(in); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetDaemonStatus()
	if err != nil {
		t.Fatal(err)
	}
	if got != in {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, in)
	}

	// A second write updates the singleton in place, not a second row.
	in.LastHeartbeatAt = 3000
	in.Mode = "polling-only"
	if err := db.SetDaemonStatus(in); err != nil {
		t.Fatal(err)
	}
	got, err = db.GetDaemonStatus()
	if err != nil {
		t.Fatal(err)
	}
	if got.LastHeartbeatAt != 3000 || got.Mode != "polling-only" {
		t.Fatalf("update not applied: %+v", got)
	}
}

func TestActivityRingCap(t *testing.T) {
	db := openTemp(t)

	// Distinct paths: an entry identical to the newest one folds into it by
	// design (W19-2), so a ring-cap test has to append distinct entries or it
	// would be measuring the fold instead.
	total := activityCap + 25
	for i := 0; i < total; i++ {
		if err := db.AppendActivity(Activity{TS: int64(i), Kind: "upload", RelPath: fmt.Sprintf("f%d", i)}); err != nil {
			t.Fatal(err)
		}
	}

	all, err := db.RecentActivity(total + 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != activityCap {
		t.Fatalf("ring not capped: want %d rows, got %d", activityCap, len(all))
	}
	// Newest first, and the oldest 25 were dropped.
	if all[0].TS != int64(total-1) {
		t.Fatalf("newest row TS = %d, want %d", all[0].TS, total-1)
	}
	if all[len(all)-1].TS != int64(total-activityCap) {
		t.Fatalf("oldest surviving TS = %d, want %d", all[len(all)-1].TS, total-activityCap)
	}
}

func TestActivitySourceRoundTrip(t *testing.T) {
	db := openTemp(t)
	if err := db.AppendActivity(Activity{TS: 1, Kind: "upload", RelPath: "a.txt", Source: "local"}); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendActivity(Activity{TS: 2, Kind: "download", RelPath: "b.txt", Source: "remote"}); err != nil {
		t.Fatal(err)
	}
	got, err := db.RecentActivity(2)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Source != "remote" || got[1].Source != "local" {
		t.Fatalf("source not preserved: %q, %q", got[0].Source, got[1].Source)
	}
}

func TestRecentActivityLimit(t *testing.T) {
	db := openTemp(t)
	for i := 0; i < 10; i++ {
		if err := db.AppendActivity(Activity{TS: int64(i), Kind: "download", RelPath: fmt.Sprintf("f%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.RecentActivity(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("limit not honored: got %d rows", len(got))
	}
	if got[0].TS != 9 {
		t.Fatalf("newest TS = %d, want 9", got[0].TS)
	}
}
