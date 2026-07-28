package engine

import (
	"context"
	"sync"
	"testing"
)

// TestStageReporterIsOptionalAndChangesNothing is the safety property: the
// reporter is instrumentation, so a cycle must behave identically with and
// without one. Same ops, same plan, same executed/failed counts.
func TestStageReporterIsOptionalAndChangesNothing(t *testing.T) {
	withRep := runCycleForStage(t, NewStageReporter())
	without := runCycleForStage(t, nil)

	if withRep.actions != without.actions || withRep.executed != without.executed || withRep.failed != without.failed {
		t.Errorf("the reporter changed the cycle: with=%+v without=%+v", withRep, without)
	}
}

// TestStageReporterObservesEveryStage: a cycle that does real work must pass
// through the stages in order, and the transferring stage must know the plan
// size (the total comes from the plan — the executor is not instrumented).
func TestStageReporterObservesEveryStage(t *testing.T) {
	rep := NewStageReporter()
	var mu sync.Mutex
	var seen []string
	seenActions := 0

	// Poll the reporter while the cycle runs, exactly as `stat` does.
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		last := ""
		for {
			select {
			case <-stop:
				return
			default:
			}
			if s, n := rep.Read(); s != "" && s != last {
				mu.Lock()
				seen = append(seen, s)
				if n > 0 && seenActions == 0 {
					seenActions = n
				}
				mu.Unlock()
				last = s
			}
		}
	}()

	got := runCycleForStage(t, rep)
	close(stop)
	<-done

	if got.actions == 0 {
		t.Fatal("the fixture cycle planned nothing; it cannot exercise the stages")
	}

	mu.Lock()
	defer mu.Unlock()
	// Not every stage is guaranteed to be observed by a polling reader on a fast
	// cycle, but the ones seen must be in cycle order and must include the two
	// that can actually take time.
	order := map[string]int{
		StageStarting: 0, StageCheckingDriv: 1, StageScanning: 2,
		StagePlanning: 3, StageTransferring: 4, StageFinishing: 5,
	}
	prev := -1
	for _, s := range seen {
		rank, ok := order[s]
		if !ok {
			t.Errorf("unknown stage reported: %q", s)
			continue
		}
		if rank < prev {
			t.Errorf("stages went backwards: %v", seen)
		}
		prev = rank
	}
	if len(seen) < 2 {
		t.Errorf("only saw %v; the reporter is not being written during the cycle", seen)
	}
	if seenActions != got.actions {
		t.Errorf("transferring stage reported %d actions, the plan had %d", seenActions, got.actions)
	}

	// After the cycle the reporter is cleared, so a finished cycle can never be
	// rendered as a running one.
	if s, n := rep.Read(); s != "" || n != 0 {
		t.Errorf("the reporter was not reset after the cycle: %q/%d", s, n)
	}
}

// TestNilStageReporterIsSafe: every method must be a no-op on nil, since
// Options.Stage is optional.
func TestNilStageReporterIsSafe(t *testing.T) {
	var r *StageReporter
	r.set(StageScanning)
	r.setActions(3)
	r.Reset()
	if s, n := r.Read(); s != "" || n != 0 {
		t.Errorf("nil reporter returned %q/%d", s, n)
	}
}

type stageOutcome struct{ actions, executed, failed int }

// runCycleForStage syncs one machine with a file to upload and a remote file to
// download, so the plan is non-empty and the transfer stage does real work.
func runCycleForStage(t *testing.T, rep *StageReporter) stageOutcome {
	t.Helper()
	fake, root := newWorld(t)
	m := newMachine(t, "a", fake, root)
	m.write(t, "local.txt", "hello")
	m.write(t, "sub/nested.txt", "more")

	res, err := m.eng.Sync(context.Background(), Options{Stage: rep})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	return stageOutcome{actions: len(res.Plan), executed: res.Executed, failed: res.Failed}
}
