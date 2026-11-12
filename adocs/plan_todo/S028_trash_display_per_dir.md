id:         S028
goal:       Linux: `status`, `doctor`, `info` and the daemon's start line describe the bin
            the sync dir's deletions really reach. S016 fixed the guard and the executor
            (`Trasher.AvailableFor(syncDir)`); these four still call `trash.Available()` /
            `trash.Describe()`, i.e. the home trash (`internal/status/status.go:175`,
            `internal/doctor/doctor.go:93`, `cmd/synckeeper/info.go:110`,
            `internal/watch/watch.go:128`). With the sync dir on another filesystem whose
            `<mount>/.Trash-$uid` is unusable they say "system bin at ~/.local/share/Trash"
            while deletions go to the quarantine and a mass deletion is held.
accepts:    With that layout all four say there is no usable bin and deletions go to the
            quarantine; with a usable volume trash they name `<mount>/.Trash-$uid`.
            Red-first tests (the `deviceOf` seam in `internal/trash`, the injected
            `SystemBin` in `internal/status`). MANUAL §8 row leaves the list; testing.md
            S016.5 closed.
touches:    internal/trash (a per-dir Describe), internal/status, internal/doctor,
            internal/watch, cmd/synckeeper (info), MANUAL.md, adocs/testing.md
excludes:   the guard and the executor (S016); macOS (Finder resolves per-volume .Trashes).
closes:
paused_by:
author:
done:
