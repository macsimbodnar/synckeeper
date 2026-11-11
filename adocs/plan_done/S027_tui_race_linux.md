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
author:     Claude (agent), started 2026-11-12 on `pop-os` — Max: "Fix ora, p.Quit()"
done:       2026-11-12, on `pop-os`. `tui.Run` drops `tea.WithContext`; a goroutine calls
            `p.Quit()` when `ctx` is done (exits when `Run` returns), so bubbletea takes
            its graceful shutdown, which waits (≤500 ms) for the read loop before
            closing the reader. Test unchanged. Red first, separate processes: 23 of 46
            runs DATA RACE before; after, 20/20 of the test and 40/40 of the whole
            package clean. `make test` green; full `go test -race ./...` green on linux
            (22 packages, run niced beside the soak); `GOOS=darwin go vet`,
            `GOOS=windows go build` clean. Fast check (REVIEW): no defect — no leak or
            deadlock in any ordering (`Run` always cancels its own ctx, so `Quit` cannot
            block); comment tightened to the 500 ms bound. Not a regression, noted: a
            `Refresh` hung inside `Update` delays a SIGTERM exit — as before, since the
            event loop never saw the context mid-`Update` either. No user-visible change:
            MANUAL, README, specs checked.
