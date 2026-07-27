package tui

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuf is a writer the renderer goroutine and the test can share; a plain
// bytes.Buffer would be a data race under -race.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestRunRendersThenQuits runs the real bubbletea program — event loop, input
// parsing and all — through the Input/Output seams, so the dashboard is proven
// to start, draw a frame built from Refresh, and exit on `q` without a
// terminal. The golden tests cover what a frame looks like; this covers that
// the program actually runs one. Input is an os.Pipe because bubbletea's
// cancellable reader wants a real file descriptor.
func TestRunRendersThenQuits(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()

	out := &syncBuf{}
	var mu sync.Mutex
	refreshed := 0

	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), Options{
			Width: 100, Height: 30,
			Interval: 10 * time.Millisecond,
			Input:    pr,
			Output:   out,
			Refresh: func() Snapshot {
				mu.Lock()
				refreshed++
				mu.Unlock()
				return testSnapshot(testRef())
			},
		})
	}()

	// Switch to the activity view, then quit.
	time.Sleep(200 * time.Millisecond)
	pw.WriteString("2")
	time.Sleep(200 * time.Millisecond)
	pw.WriteString("q")

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("the dashboard did not exit on `q`; rendered so far:\n%s", out.String())
	}
	pw.Close()

	mu.Lock()
	n := refreshed
	mu.Unlock()
	if n == 0 {
		t.Error("Run never called Refresh — the first frame would have been empty")
	}
	got := out.String()
	for _, want := range []string{"synckeeper", "max_mbp", "activity"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered output missing %q; got:\n%s", want, got)
		}
	}
}

// TestRunTreatsCancellationAsCleanExit: Ctrl-C and SIGTERM reach Run as a
// cancelled context, which is a normal way to leave a dashboard, not an error
// for the CLI to print. Input stays open (nothing written) so only the
// cancellation can end the run.
func TestRunTreatsCancellationAsCleanExit(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()

	ctx, cancel := context.WithCancel(context.Background())
	out := &syncBuf{}

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Width: 80, Height: 24,
			Interval: 10 * time.Millisecond,
			Input:    pr,
			Output:   out,
			Refresh:  func() Snapshot { return testSnapshot(testRef()) },
		})
	}()

	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("a cancelled context must exit cleanly, got %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the dashboard ignored a cancelled context")
	}
}

func TestColorEnabledRespectsEnvironment(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "1")
	if ColorEnabled() {
		t.Error("NO_COLOR must disable colour")
	}
	// Set but empty still counts, per no-color.org.
	t.Setenv("NO_COLOR", "")
	if ColorEnabled() {
		t.Error("an empty but present NO_COLOR must disable colour")
	}
	os.Unsetenv("NO_COLOR")
	if !ColorEnabled() {
		t.Error("a normal TERM with no NO_COLOR should enable colour")
	}
	t.Setenv("TERM", "dumb")
	if ColorEnabled() {
		t.Error("TERM=dumb must disable colour")
	}
}
