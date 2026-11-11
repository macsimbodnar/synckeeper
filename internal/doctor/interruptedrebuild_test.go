package doctor

// S009 (audit 2026-11-11 F03) through `doctor --repair`: its mirror rebuild
// stopped at ANY List call — by error, or by process death — must leave a
// state the next ordinary cycle reads as nothing changed. Before the fix,
// every baseline row the partial mirror had not refilled was planned as
// deleted on Drive, and the local copies went to the bin.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/engine"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// killAtList fails the at-th List call. When crash is set it first takes the
// DB's crash image: the committed state a process killed at that instant
// leaves on disk, independent of anything the error path does afterwards.
type killAtList struct {
	driveclient.Client
	at, calls int
	crash     func()
}

func (k *killAtList) List(ctx context.Context, parentID string) ([]driveclient.File, error) {
	k.calls++
	if k.calls == k.at {
		if k.crash != nil {
			k.crash()
		}
		return nil, errors.New("dial tcp: connection reset by peer")
	}
	return k.Client.List(ctx, parentID)
}

// crashImage copies the committed state of the DB at src to dst.
func crashImage(t *testing.T, src, dst string) {
	t.Helper()
	h, err := sql.Open("sqlite", src)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if _, err := h.Exec(`vacuum into ?`, dst); err != nil {
		t.Fatal(err)
	}
}

func TestS009RepairRebuildInterruptedAtEveryList(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) *world {
		t.Helper()
		w := newWorld(t)
		w.write(t, "top.txt", "top")
		for _, d := range []string{"docs", "photos", "work/deep"} {
			for _, f := range []string{"1.txt", "2.txt", "3.txt"} {
				w.write(t, d+"/"+f, d+f)
			}
		}
		w.sync(t)
		// Idle: the feed has reported this machine's own uploads.
		if res := w.sync(t); len(res.Plan) != 0 {
			t.Fatalf("setup: second cycle planned %d actions, want idle", len(res.Plan))
		}
		return w
	}

	// Every List a clean repair makes is a point to interrupt at.
	clean := setup(t)
	counter := &killAtList{Client: clean.fake}
	if _, err := (&Doctor{DB: clean.db, Client: counter, Cfg: clean.cfg, SyncDir: clean.syncDir}).Repair(ctx); err != nil {
		t.Fatal(err)
	}
	if counter.calls < 5 {
		t.Fatalf("a clean repair made %d List calls; the tree has 5 folders to walk", counter.calls)
	}

	for at := 1; at <= counter.calls; at++ {
		for _, crash := range []bool{false, true} {
			t.Run(fmt.Sprintf("list%d/crash=%v", at, crash), func(t *testing.T) {
				w := setup(t)
				before := w.tree(t)
				k := &killAtList{Client: w.fake, at: at}
				image := filepath.Join(w.base, "crash.db")
				if crash {
					k.crash = func() { crashImage(t, filepath.Join(w.base, "state.db"), image) }
				}
				if _, err := (&Doctor{DB: w.db, Client: k, Cfg: w.cfg, SyncDir: w.syncDir}).Repair(ctx); err == nil {
					t.Fatal("the interrupted repair should have reported its error")
				}
				if crash {
					db, err := statedb.Open(image)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { db.Close() })
					w.db = db
				}

				res, err := w.engine().Sync(ctx, engine.Options{})
				if err != nil {
					t.Fatalf("cycle after the interrupted rebuild: %v", err)
				}
				deletes := 0
				for _, a := range res.Plan {
					if a.Type == reconcile.QuarantineLocal {
						deletes++
					}
				}
				after := w.tree(t)
				if len(res.Plan) != 0 || len(after) != len(before) {
					t.Fatalf("nothing changed, yet the next cycle planned %d actions (%d local deletes); %d of %d local files left",
						len(res.Plan), deletes, len(after), len(before))
				}
			})
		}
	}
}
