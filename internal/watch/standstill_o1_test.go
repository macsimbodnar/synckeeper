package watch

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// S031: a plan rule O1 refuses changes nothing either, so it is a standstill
// like a §4.5 overlap — and a run of either kind of refusal is one run.
func TestARuleO1RefusalIsAStandstill(t *testing.T) {
	err := reconcile.ValidateLocalOverwrites(reconcile.Input{
		Base:  map[string]reconcile.BaseItem{"f.txt": {FileID: "F", Size: 3, MD5: "X", DriveMD5: "X"}},
		Local: map[string]reconcile.LocalItem{"f.txt": {Size: 5, MtimeNS: 2000, MD5: "EDIT"}},
	}, []reconcile.Action{
		{Type: reconcile.Download, RelPath: "f.txt", FileID: "F", LocalExists: true, LocalSize: 5, LocalMtimeNS: 2000},
	})
	var o1 reconcile.RefusedPlan
	if !errors.As(err, &o1) {
		t.Fatalf("ValidateLocalOverwrites = %v, want a reconcile.RefusedPlan", err)
	}
	l := newLiveState(time.Minute, time.Second)
	t0 := time.Unix(1_800_000_000, 0)
	l.noteRefused(t0, refusal(t, "N/f.txt"))
	if n := l.noteRefused(t0.Add(time.Minute), o1); n != 2 {
		t.Fatalf("run = %d after an overlap refusal and an O1 refusal, want 2", n)
	}
	s := l.snapshot(t0.Add(2 * time.Minute))
	if s.Standstill == nil || s.Standstill.Since != t0.Unix() || s.Standstill.Reason != o1.Error() ||
		!slices.Equal(s.Standstill.Paths, []string{"f.txt", "f.txt"}) {
		t.Errorf("standstill = %+v, want since %d, the O1 refusal's message and paths", s.Standstill, t0.Unix())
	}
}
