id:         S027
goal:       `go test -race ./...` green on Linux. `TestRunTreatsCancellationAsCleanExit`
            (`internal/tui/run_test.go:99`) reports DATA RACE in 4 of 8 runs on `pop-os`
            (go1.26.4, bubbletea v1.3.10, cancelreader v0.2.2); found by S001's L8 gate
            2026-11-12, green on darwin. Cause, by reading: `tui.Run` passes
            `tea.WithContext(ctx)`; a cancelled context makes `Program.Run` take its kill
            path (`tea.go:722` `killed := p.externalCtx.Err() != nil …`) →
            `shutdown(kill=true)` closes the cancel reader without `waitForReadLoop`
            (`tea.go:819-824`), while `readLoop`'s epoll `wait()` still reads `File.Fd()`
            (`cancelreader_linux.go:147/149`). The test's deferred `pr.Close()` races the
            same leaked goroutine. Production effect: the dashboard's exit on SIGTERM /
            context cancel — a data race at process exit, no symptom observed.
accepts:    Red first: the race reproduced under `-race` on Linux, rate stated. Fix in the
            dashboard's exit path, not in the test (never weaken it) — e.g. watch `ctx`
            and call `p.Quit()` so bubbletea takes its graceful path, which waits for the
            read loop before closing; Ctrl-C / SIGTERM still exit cleanly with no error.
            `go test -race -count=1` of `internal/tui` green in 20 of 20 separate
            processes on Linux; darwin unchanged; `go test -race ./...` green.
touches:    internal/tui (run.go, maybe run_test.go only to stop leaking the pipe)
excludes:   upgrading or patching bubbletea / cancelreader (DEPS rule: a version change is
            Max's call); any other dashboard behaviour. Blocks S001: its L8 `-race` gate.
closes:
paused_by:
author:
done:
