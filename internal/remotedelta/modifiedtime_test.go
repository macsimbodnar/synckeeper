package remotedelta

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// W18.10 — Drive's modifiedTime survives the whole trip: API reply → mirror
// row → snapshot. The planner's newer-wins rule (W18-E) is only as good as
// this pipe, and a stamp dropped anywhere along it would leave the rule
// looking implemented while silently always falling back to remote-wins.
func TestModifiedTimeReachesTheSnapshot(t *testing.T) {
	ctx := context.Background()
	fake := driveclient.NewFake()
	stamp := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	fake.Now = func() time.Time { return stamp }
	db, rootID := newCache(t, fake)
	if _, err := fake.Upload(ctx, rootID, "a.txt", strings.NewReader("hello"), 5); err != nil {
		t.Fatal(err)
	}

	// Both mirror writers must carry it: the changes feed here, and the forced
	// full walk below — which is what `init`, the only reader of the stamp,
	// runs before it reads. That is also why the v5 migration needs no
	// backfill.
	refresh(t, fake, db, rootID)
	assertStamped(t, db, rootID, stamp)

	if err := ForceFullWalk(ctx, fake, db, rootID); err != nil {
		t.Fatal(err)
	}
	assertStamped(t, db, rootID, stamp)
}

func assertStamped(t *testing.T, db *statedb.DB, rootID string, want time.Time) {
	t.Helper()
	snap, _, err := Snapshot(db, rootID, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := snap["a.txt"]
	if !ok {
		t.Fatalf("a.txt missing from the snapshot: %+v", snap)
	}
	if item.ModifiedNS != want.UnixNano() {
		t.Errorf("ModifiedNS = %d (%s), want %d (%s)",
			item.ModifiedNS, time.Unix(0, item.ModifiedNS).UTC(), want.UnixNano(), want)
	}
}

// A Drive reply without a modifiedTime must leave the stamp at 0 — "not
// known" — and never at a huge negative number, which is what UnixNano() on
// the zero time gives and which would read as "older than everything".
func TestAnAbsentStampStaysZero(t *testing.T) {
	n := toNode(driveclient.File{ID: "x", Name: "x.txt"}, "parent")
	if n.ModifiedNS != 0 {
		t.Errorf("ModifiedNS = %d, want 0 for a file Drive reported no modifiedTime for", n.ModifiedNS)
	}
}
