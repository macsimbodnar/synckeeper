id:         S013
goal:       A tracked folder replaced by a symlink has its Drive folder trashed and is
            removed from other machines. `internal/scanner/scanner.go:68-71` reports
            the symlink as a skip and drops the path from the snapshot; only
            `Skip.Unreadable` is held harmless (`internal/engine/engine.go:358-378`).
            Found by audit 2026-11-11_adversarial (F07, Medium).
accepts:    `TestAudit20261111_F07_FolderReplacedBySymlinkIsTrashedOnDrive` passes
            ungated: 3 of 3 files left on Drive and on machine b, the skip reported.
            MANUAL §8 row leaves the list in the fix's commit; testing.md row lands
            with the fix.
touches:    internal/scanner, internal/engine, MANUAL.md, adocs/specs.md §5,
            adocs/testing.md
excludes:   Following symlinks. Surfacing skips under the daemon (S003).
closes:     2026-11-11_adversarial-F07
paused_by:
author:     Opus subagent, coordinated by Claude (agent); started 2026-11-11 at Max's direction (DEC-002)
done:       2026-11-11. `TestAudit20261111_F07_FolderReplacedBySymlinkIsTrashedOnDrive` passes
            ungated (3 of 3 on Drive and on b) and now also asserts the skip is reported.
            The scanner marks symlink / non-regular skips `Unscanned`; the planner holds
            every tracked path that is, at that same path (case/normalization-folded),
            such an entry, and everything under it: no action on either side, baseline
            rows kept, each reported as a skip; a folder is not removed on either side
            while it contains a link or a held row's item. New structural net in the
            executor's local write gate: `refuseLinkedPaths` refuses any local mutation
            with a symlink component below the sync root (AST-tested, 9 behaviour
            cases). Tests: testing.md S013.1–S013.14. Verified by the coordinator after
            each of three rounds: `go build && go vet`, `gofmt -l`, `go test -count=1
            ./...`, `-race` on scanner/reconcile/executor green on darwin; `GOOS=linux
            go build/vet` clean; 60×150 fuzz fails on seeds 1, 28, 45 only (S002).
            Three rounds, two fast checks (REVIEW). Round 1 → review found the hold
            ignored folding (a download could write through a link) and missed a parent
            rename. Round 2 fixed both, but with a "same name anywhere" rule that the
            second review showed turns ordinary moves into copies and folder renames
            into full re-uploads in any tree with unrelated links (.app bundles, venvs,
            `node_modules/.bin`). Round 3: that rule REMOVED by the coordinator's call;
            a further hole fixed (a held file moved on Drive into a folder deleted here
            went to the bin with it; and its mirror).
            NOT closed, MANUAL §8 carries one row: the Drive folder is still trashed
            when the link is renamed or replaced by a differently named one, when its
            parent is renamed with nothing beside the link to pair the rename, when both
            are renamed, and — the likely one — when the swap is done in steps while the
            daemon runs (a cycle in the gap reads a real deletion). Deleting the link is
            a deletion by design (MANUAL §6). → S025. Under the daemon the hold is
            reported nowhere (S003).
