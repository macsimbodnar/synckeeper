id:         S029
goal:       The `internal/watch` tests reach the developer's real system bin. Their
            `newMachine` (`internal/watch/watch_test.go:38`) builds an `engine.Engine`
            with no `Trash`, so `engine.defaultTrash = trash.OS()` applies — the suite
            rule "no test may ever move a file into the developer's own trash" is
            enforced by `TestMain` in `internal/engine` and `internal/executor` only.
            `TestSoak` propagates chaos deletions between its two machines as
            `quarantine_local`, i.e. `MoveToTrash` into `~/.local/share/Trash` (Linux,
            sync dir under `/tmp` on the home filesystem) or Finder's Trash (darwin+cgo):
            measured: 16,455 items (131 MB) in S001's 2 h L8 run (testing.md S001
            L8.5). Found 2026-11-12 reading the soak before S001's L8 run; that
            run was isolated with `XDG_DATA_HOME` pointed at a scratch dir. No test
            artifact found in `pop-os`'s real trash.
accepts:    No `internal/watch` test can reach the real bin on any platform: a decision
            between a `trash.Fake` in `newMachine` (simple; the soak then no longer
            exercises the real freedesktop/Finder bin) and an isolated real bin
            (`XDG_DATA_HOME` in `TestMain` — Linux only, darwin has no equivalent);
            a guard test proving it; the soak command in `CLAUDE.md` unchanged or
            updated in the same commit.
touches:    internal/watch (tests only), maybe CLAUDE.md
excludes:   any production code.
closes:
paused_by:
author:
done:
