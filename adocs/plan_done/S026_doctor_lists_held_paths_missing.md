id:         S026
goal:       `doctor` reports a held path as missing locally. `internal/doctor/doctor.go:111-121`
            discards the scan's skips, so a tracked path that is on disk but unscanned —
            a symlink or non-regular file (S013), an unreadable file (S014) — lands in
            `MissingLocal`. Report-only: `--repair` adopts only items present locally and
            does not act on it. Found by S014's fast check (2026-11-11), by reading.
accepts:    A red-first doctor test: a tracked file replaced by a symlink, and one made
            unreadable, are reported as held/skipped with the scan's reason, not as
            missing. MANUAL §3/§7 if `doctor`'s output wording changes; testing.md row.
touches:    internal/doctor, cmd/synckeeper (doctor output), MANUAL.md, adocs/testing.md
excludes:   Any change to what `--repair` does.
closes:
paused_by:
author:     Opus subagent, coordinated by Claude (agent); started 2026-11-11 at Max's direction (DEC-002)
done:       2026-11-11. `doctor`'s check keeps the scan's skips; a row absent from the
            snapshot that the engine holds — at or under a `Skip.Unscanned` path
            (fold-aware), or under a `Skip.Unreadable` directory — goes to the new
            `Report.HeldLocal` with the scan's reason and is printed under "rows on
            disk but not read by the scan (held)", not under missing. `Healthy()`
            counts it, so the verdict is unchanged. `Repair` untouched.
            Tests `TestS026…` ×5 (testing.md S026.1–S026.6): symlinked file, symlinked
            folder, unreadable file, rows under an unreadable dir, a really-gone file
            still missing, `--repair` identical on each world, the printed section.
            Verified by the coordinator: `go build && go vet`, `gofmt -l`,
            `go test -count=1 ./...`, `-race` on doctor/cmd green on darwin;
            `GOOS=linux go build/vet` clean. Fast check (REVIEW, small): nothing found.
            Gap, stated in the code comment: holds the planner infers through move
            detection (S014's stat match; a row renamed into an unreadable folder) are
            not reproduced — such an old-path row still shows as missing in `doctor`
            though sync holds it. Noted, not reproduced: `expandUnreadable` matches by
            exact prefix while the planner's holds fold names, so on a case-folding
            disk a baseline path whose case differs from the unreadable folder's would
            not be held by W18-G.
