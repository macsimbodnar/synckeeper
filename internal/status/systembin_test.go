package status

import (
	"strings"
	"testing"
)

// W14-M2: an absent system bin is the one condition under which a mass
// deletion is still held, so `status` and `doctor` must say so plainly —
// naming the fallback destination and the flag that releases it — rather than
// leaving the user to discover it from a blocked cycle.
//
// Moved here from cmd/synckeeper with W15-U1, unchanged, when SystemBinLine
// became the shared formatter for `status`, `doctor` and the dashboard.
func TestSystemBinLine(t *testing.T) {
	if got := SystemBinLine(true, "freedesktop trash at /home/max/.local/share/Trash"); got != "freedesktop trash at /home/max/.local/share/Trash" {
		t.Errorf("available bin line = %q, want just the destination", got)
	}
	got := SystemBinLine(false, "unavailable (no home directory)")
	for _, want := range []string{"UNAVAILABLE", "quarantine", "--confirm-deletes"} {
		if !strings.Contains(got, want) {
			t.Errorf("absent bin line %q is missing %q", got, want)
		}
	}
}
