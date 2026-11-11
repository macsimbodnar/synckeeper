id:         S019
goal:       The remote-write gate (S010) has never run against real Drive, and its
            real-client window is wider than it needs to be. (1) `real.Update`
            (`internal/driveclient/real.go`) checks the precondition before the media
            transfer, so for a large file the unprotected window is the whole upload —
            another machine's edit landing meanwhile is buried in Drive's version
            history with no conflict copy (the F04 loss, for the files where it hurts
            most). (2) `real.Trash`/`real.Move` check once, then mutate inside
            `withRetry`; a retried mutation lands up to the full backoff later,
            unchecked. (3) The fake marks only the trashed item `Trashed`; real Drive
            reports `trashed: true` on everything inside a trashed folder — so the
            "item inside a folder another machine binned" refusal and a possible
            one-cycle self-refusal (a folder trash sorted ahead of its children's,
            `plan.go:750` vs `:550`/`:690`) exist only on real Drive and are untested.
            Found by S010's fast check (2026-11-11), by reading.
accepts:    A live run (`SYNCKEEPER_LIVE_TEST=1`) exercises each gated mutation and each
            drift kind against a throwaway Drive folder and records what real Drive
            returns. The check is repeated per retry attempt for `Trash`/`Move`. For
            `Update`, an edit landing during the upload is detected afterwards (e.g. the
            head revision before vs the revision under ours) and kept as a conflict
            copy — or Max accepts the window and MANUAL §6 keeps saying so. The fake
            marks descendants of a trashed folder the way real Drive does. testing.md
            rows; specs §7; MANUAL §6.
touches:    internal/driveclient (real, fake), internal/executor, MANUAL.md,
            adocs/specs.md, adocs/testing.md
excludes:   Nothing is built before the live measurement: Max's credentials and a
            throwaway folder are needed, and the shape of the post-upload check
            (extra API calls per upload) is Max's pick.
closes:
paused_by:
author:
done:
