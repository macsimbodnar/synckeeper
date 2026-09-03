id:         S005
goal:       "2 local changes — syncing" counts watcher wake-ups, not files.
            `liveState.pendingChanges` (`internal/watch/live.go:141`) increments once
            per FSEvents callback batch; `internal/tui/view.go:313` renders that
            number as "N local changes". Measured 2026-09-03 on the real backend: one
            8 MiB file written over ~1.2 s produces 2 wakes, so one file reads as
            "2 local changes". Max read it as two files queued for Drive; the cycle
            that followed did 0 actions.
accepts:    The dashboard line no longer claims a file count it does not have —
            either it names what is counted (file events) or the count is dropped.
            The wording is Max's call. A tui test pins the new text. testing.md row
            lands with the fix; MANUAL updated if it repeats the old wording.
touches:    internal/tui/view.go, internal/tui/live_test.go (asserts "3 local changes
            — syncing" today), adocs/testing.md, MANUAL.md
excludes:   Changing what `pendingChanges` counts — per-file counting would need the
            watcher to track paths, which it deliberately does not (the increment
            rides inside the existing mutex, no per-event work).
closes:
paused_by:
author:
done:
