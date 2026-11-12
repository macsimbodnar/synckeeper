id:         S034
goal:       A one-shot `synckeeper sync` run while the daemon is running delegates the
            cycle to it (`delegateSync`, cmd/synckeeper) and checks for a changed cycle
            summary before it checks `last error` — so a cycle the daemon refused
            (§4.5) may print `Done.` instead of the error. From code reading by S002's
            implementer (2026-11-12); not run.
accepts:    Reproduced first (a test that makes the daemon's cycle fail or be refused
            while `sync` delegates); then `sync` prints the error and exits non-zero.
            MANUAL §3 if the described behaviour changes; testing.md row.
touches:    cmd/synckeeper, internal/watch (control), MANUAL.md, adocs/testing.md
excludes:   S003, S004, S005 (neighbours).
closes:
paused_by:
author:
done:
