id:         S018
goal:       A folder moved or restored into the Drive tree whose content walk fails once
            is never walked again. `consumeChanges` (`internal/remotedelta/remotedelta.go:131-147`)
            upserts the newly seen folder's row inside the batch loop and walks its
            contents only after the loop; if that walk fails (error or crash) the page
            token is not saved and the batch replays — but `HasRemoteNode` is now true,
            so the walk is never queued again. The folder appears locally, its contents
            never download, no error, later cycles look idle. Deeper changes do not heal
            it: a change under a still-unknown subfolder is upserted under an unknown
            parent and pruned as unreachable. Found by S009's implementer by reading,
            reproduced by S009's fast check with a scratch test (2026-11-11): folder
            `box` with `inner.txt` and `sub/deep.txt` moved into the root, `List(box)`
            failing once → snapshot stays `[box]` after two more `Refresh` calls and
            after `deep.txt` is edited on Drive.
accepts:    A red-first test in `internal/remotedelta` with that exact shape, and an
            engine-level one asserting the contents download on the cycle after the
            failed one. A crash at the same point (not only a returned error) heals the
            same way. Installs already holding such a hole are healed or the cure is
            stated (today: `doctor --repair`, which force-walks). testing.md rows; MANUAL
            §8 row leaves the list in the fix's commit; specs §9.
touches:    internal/remotedelta, internal/statedb (only if pending walks are persisted
            — a schema change is reported to Max before it is built), MANUAL.md,
            adocs/specs.md, adocs/testing.md
excludes:   Retrying network-level errors in `internal/driveclient/retry.go`. The
            deletion route sketched by the reviewer (folder moved out, cycle pruned but
            its local deletes not run, folder moved back, re-walk fails once → baseline
            rows plan local deletes) is a guess, not reproduced — write the test for
            it; if it is real it is in scope, if not say so in the stamp.
closes:
paused_by:
author:     Opus subagent, coordinated by Claude (agent); started 2026-11-11 at Max's direction (DEC-002)
done:
