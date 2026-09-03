id:         S004
goal:       Stored error details keep the URL and lose the cause. `internal/watch/status.go:22`
            caps one activity detail at 400 runes and `status.Clip`
            (`internal/status/format.go:68`) keeps the head, but a wrapped Drive
            `changes.list` error opens with ~360 characters of query string — so the
            part that names the failure is cut off before it reaches the DB. Found in
            the field 2026-09-03: six consecutive error rows on Max's dashboard, every
            one unreadable.
accepts:    An error from `changes.list` stores a detail whose cause is present and
            readable — e.g. `http2: client connection lost`, `401 Invalid
            Credentials` — within the 400-rune cap. Verified red-first against a
            recorded real error string from the field. testing.md row lands with the
            fix.
touches:    internal/engine/engine.go:170 (or internal/remotedelta, wherever the URL
            enters the wrap), internal/watch/status.go, internal/status/format.go,
            adocs/testing.md
excludes:   Raising `maxStoredText` — the database is not a log, and the cap is not
            the defect. Rows already stored truncated cannot be recovered.
closes:
paused_by:
author:
done:
