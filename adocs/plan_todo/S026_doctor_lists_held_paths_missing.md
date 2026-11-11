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
author:
done:
