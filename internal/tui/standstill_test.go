package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/status"
)

// S002: the attention block names a standstill — a plan refused cycle after
// cycle — apart from the `last error` that repeats with it, and says what the
// user can do.
func TestTheAttentionBlockNamesAStandstill(t *testing.T) {
	ref := testRef()
	m := backoffModel(ref)
	m.snap.Status.Daemon.LastError = `plan violates §4.5: download and upload both act on "N/f.txt" in the concurrent transfer stage; refusing the plan, replanning next cycle`
	m.snap.Status.Standstill = &status.Standstill{Since: ref.Add(-12*time.Minute - 5*time.Second).Unix(), Cycles: 4,
		Paths: []string{"N/f.txt"}, Reason: m.snap.Status.Daemon.LastError}
	m.width = 220 // wide enough that nothing is cut
	frame := m.View()
	for _, want := range []string{
		`⚠ syncing is stuck — for 12m, 4 cycles in a row: the plan is refused (§4.5) on "N/f.txt"; nothing syncs in either direction`,
		"  " + status.StandstillHint,
		"⚠ last error — plan violates §4.5",
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("the frame lacks %q:\n%s", want, frame)
		}
	}
	if i, j := strings.Index(frame, "syncing is stuck"), strings.Index(frame, "⚠ last error"); i < 0 || j < i {
		t.Errorf("the standstill is not above the error it explains:\n%s", frame)
	}

	m.snap.Status.Standstill = nil
	if frame := m.View(); strings.Contains(frame, "syncing is stuck") {
		t.Errorf("no standstill reported, yet the frame names one:\n%s", frame)
	}
}
