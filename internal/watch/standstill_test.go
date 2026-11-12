package watch

// S002, part 2: a plan the §4.5 check refuses cycle after cycle is a
// standstill — nothing syncs either way and retrying changes nothing — and the
// daemon must say so, not leave it to a `last error` that merely repeats. The
// refusal is injected (Watcher.syncCycle): these tests must not depend on a
// planner bug existing.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/control"
	"github.com/macsimbodnar/synckeeper/internal/engine"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// refusal is a real refusal from the real check, on a crafted plan.
func refusal(t *testing.T, rel string) *reconcile.TransferOverlapError {
	t.Helper()
	err := reconcile.ValidateTransferStage([]reconcile.Action{
		{Type: reconcile.Upload, RelPath: rel},
		{Type: reconcile.Download, RelPath: rel, FileID: "F"},
	})
	var e *reconcile.TransferOverlapError
	if !errors.As(err, &e) {
		t.Fatalf("ValidateTransferStage = %v, want a *TransferOverlapError", err)
	}
	return e
}

// A double conflict on one target is refused with the conflict copy's path
// first, and that path carries a per-second timestamp: the message differs
// every cycle. The run must still grow (round-2 review, S002).
func TestARefusalWhoseMessageChangesEveryCycleIsStillAStandstill(t *testing.T) {
	l := newLiveState(time.Minute, time.Second)
	t0 := time.Unix(1_800_000_000, 0)
	for i := 0; i < 3; i++ {
		cp := fmt.Sprintf("f (conflict a 2026-11-12_0000%02d).txt", i)
		err := reconcile.ValidateTransferStage([]reconcile.Action{
			{Type: reconcile.Upload, RelPath: cp},
			{Type: reconcile.Upload, RelPath: cp},
			{Type: reconcile.Download, RelPath: "f.txt", FileID: "F"},
		})
		var e *reconcile.TransferOverlapError
		if !errors.As(err, &e) {
			t.Fatalf("cycle %d: %v, want a refusal", i, err)
		}
		l.noteRefused(t0.Add(time.Duration(i)*time.Minute), e)
	}
	s := l.snapshot(t0.Add(3 * time.Minute))
	if s.Standstill == nil || s.Standstill.Cycles != 3 || s.Standstill.Since != t0.Unix() {
		t.Fatalf("three refused cycles, messages all different: standstill = %+v, want 3 cycles since %d", s.Standstill, t0.Unix())
	}
	if want := "f (conflict a 2026-11-12_000002).txt"; !slices.Equal(s.Standstill.Paths, []string{want}) {
		t.Errorf("paths = %v, want the latest cycle's [%s]", s.Standstill.Paths, want)
	}
}

func TestLiveStateRefusalRun(t *testing.T) {
	l := newLiveState(time.Minute, time.Second)
	t0 := time.Unix(1_800_000_000, 0)
	a, b := refusal(t, "N/f.txt"), refusal(t, "M/g.txt")

	if n := l.noteRefused(t0, a); n != 1 {
		t.Fatalf("first refusal: run = %d, want 1", n)
	}
	if s := l.snapshot(t0); s.Standstill != nil {
		t.Fatalf("one refused cycle is an ordinary failure, got standstill %+v", s.Standstill)
	}
	l.noteRefused(t0.Add(time.Minute), a)
	s := l.snapshot(t0.Add(2 * time.Minute))
	if s.Standstill == nil {
		t.Fatal("two refused cycles in a row, same reason: no standstill published")
	}
	if got := *s.Standstill; got.Since != t0.Unix() || got.Cycles != 2 ||
		!slices.Equal(got.Paths, []string{"N/f.txt"}) || got.Reason != a.Error() {
		t.Errorf("standstill = %+v, want since %d, 2 cycles, [N/f.txt], %q", got, t0.Unix(), a.Error())
	}

	// Any refusal extends the run: a refused cycle syncs nothing, whatever it
	// names. The latest cycle's paths and message are shown.
	l.noteRefused(t0.Add(3*time.Minute), b)
	if s := l.snapshot(t0.Add(3 * time.Minute)); s.Standstill == nil || s.Standstill.Since != t0.Unix() ||
		s.Standstill.Cycles != 3 || !slices.Equal(s.Standstill.Paths, []string{"M/g.txt"}) || s.Standstill.Reason != b.Error() {
		t.Errorf("third refusal, other paths: standstill = %+v, want since %d, 3 cycles, the latest paths and message", s.Standstill, t0.Unix())
	}

	l.notePlanned()
	if s := l.snapshot(t0.Add(5 * time.Minute)); s.Standstill != nil {
		t.Errorf("a cycle that planned normally kept the standstill: %+v", s.Standstill)
	}
}

// End to end over the real control socket: the daemon refuses its plan two
// cycles running, `stat` says so, and the first cycle that plans normally
// withdraws it.
func TestStatPublishesAStandstillAndClearsIt(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	stuck := refusal(t, "N/f.txt")

	var refusing atomic.Bool
	refusing.Store(true)
	var refused atomic.Int32
	dir, err := os.MkdirTemp("/tmp", "skwatch")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "c.sock")
	w := &Watcher{Eng: a.eng, Poll: 50 * time.Millisecond, Debounce: 50 * time.Millisecond, ControlSocket: sock,
		syncCycle: func(ctx context.Context, opts engine.Options) (*engine.Result, error) {
			if refusing.Load() {
				refused.Add(1)
				return &engine.Result{}, stuck
			}
			return a.eng.Sync(ctx, opts)
		}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := w.Run(ctx); err != nil {
			t.Errorf("watcher: %v", err)
		}
	}()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, "control socket up", 5*time.Second, func() bool {
		resp, err := control.Call(sock, control.Request{Cmd: control.CmdPing})
		return err == nil && resp.OK
	})
	stat := func() LiveSnapshot {
		resp := call(t, sock, control.CmdStat, nil)
		var got LiveSnapshot
		if err := json.Unmarshal(resp.Data, &got); err != nil {
			t.Fatalf("stat payload: %v", err)
		}
		return got
	}

	var s LiveSnapshot
	waitFor(t, "the daemon publishes the standstill", 10*time.Second, func() bool {
		s = stat()
		return s.Standstill != nil
	})
	if n := refused.Load(); n < 2 {
		t.Errorf("standstill published after %d refused cycle(s), want 2 or more", n)
	}
	st := *s.Standstill
	if st.Cycles < 2 || !slices.Equal(st.Paths, []string{"N/f.txt"}) || st.Reason != stuck.Error() {
		t.Errorf("standstill = %+v, want ≥ 2 cycles on [N/f.txt] with the refusal's message", st)
	}
	if now := time.Now().Unix(); st.Since <= 0 || st.Since > now {
		t.Errorf("since = %d, want a past instant (now %d)", st.Since, now)
	}

	// The plan changes (here: the injection stops); an explicit sync cuts the
	// backoff short, and the cycle that plans normally clears the standstill.
	refusing.Store(false)
	call(t, sock, control.CmdSync, nil)
	waitFor(t, "the standstill is withdrawn", 10*time.Second, func() bool {
		s = stat()
		return s.Standstill == nil && !s.BackingOff
	})
}
