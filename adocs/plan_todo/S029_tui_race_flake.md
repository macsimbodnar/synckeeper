id:         S029
goal:       `TestRunTreatsCancellationAsCleanExit` in `internal/tui` is flaky under
            `-race`: it sometimes reports a data race inside bubbletea's cancelreader.
            Seen by S002's implementer about once in 6 runs, and on an untouched copy of
            `5f61255` (run 18 of 25) — pre-existing. "Green at every commit" cannot rest
            on a test that fails one run in six.
accepts:    The cause is located (our use of the program's lifecycle, or the library);
            the test is made deterministic without weakening what it asserts, or the
            race is fixed; 50 consecutive `go test -race -count=1 ./internal/tui` runs
            pass. If it is a library defect: recorded as a decision, with the version.
touches:    internal/tui
excludes:   Adding or upgrading a dependency without asking (DEPS).
closes:
paused_by:
author:
done:
