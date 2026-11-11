id:         S010
goal:       A local edit's upload overwrites a concurrent remote edit — last writer wins,
            no conflict copy (INV-5). `internal/executor/executor.go:625` calls
            `Update` with no check that Drive still holds the version the plan was
            built on; `Trash` (`:763`) and `Move` (`:536`) have none either. The
            window is refresh → mutation (scan plus transfer queue). Found by audit
            2026-11-11_adversarial (F04, High).
accepts:    `TestAudit20261111_F04_UpdateRemoteOverwritesConcurrentRemoteEdit` passes
            ungated: both edits survive, one as a conflict copy. The same holds for a
            `TrashRemote` and a `MoveRemote` racing a remote edit (new red-first
            tests). MANUAL §8 row leaves the list in the fix's commit; testing.md row
            lands with the fix.
touches:    internal/executor, internal/reconcile (action carries the expected remote
            version), internal/driveclient (real + fake), MANUAL.md, adocs/specs.md §7,
            adocs/testing.md
excludes:   Closing the window to zero — a `Get` before the mutation narrows it to one
            round trip; a true server-side precondition is a separate question. The
            window's size on real Drive is unmeasured (fake only).
closes:     2026-11-11_adversarial-F04
paused_by:
author:     Opus subagent, coordinated by Claude (agent); started 2026-11-11 at Max's direction (DEC-002)
done:
