package watch

import "testing"

func TestFailureLatch(t *testing.T) {
	var l failureLatch

	// Failures below the threshold never latch.
	for i := 0; i < watchFailureLatch-1; i++ {
		if l.note(5) {
			t.Fatalf("latched after %d consecutive failures (threshold %d)", i+1, watchFailureLatch)
		}
	}
	// A success resets the count.
	if l.note(0) {
		t.Fatal("latched on success")
	}
	for i := 0; i < watchFailureLatch-1; i++ {
		if l.note(1) {
			t.Fatal("latched before threshold after reset")
		}
	}
	if !l.note(1) {
		t.Fatalf("did not latch at %d consecutive failures", watchFailureLatch)
	}
}
