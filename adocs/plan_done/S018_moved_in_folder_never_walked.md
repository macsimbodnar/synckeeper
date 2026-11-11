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
done:       2026-11-11. `consumeChanges` holds the row of a folder entering the tree in
            memory and writes it only after that folder's walk completes; the page token
            is still saved last. "Entering" = the new parent is linked to the root
            through mirror rows (`inTree`, the reachability `prune` and `Snapshot` use)
            and the folder's own cached row is not — row existence alone is not
            trusted. New read-only `(*DB).RemoteParent`. No schema change, no meta key.
            Tests (testing.md S018.1–S018.8): the exact reproduction shape; a fault at
            every `List` as error and as crash image; a partly walked subfolder moved in
            directly; a folder seen twice in one batch; a restored folder with a legacy
            trashed tombstone; the engine-level download after a failed walk.
            The deletion route `excludes` asked to test is REAL and fixed: a folder
            moved out and back with its re-walk interrupted planned local deletes of
            its files (12 fault cases red, `TestS018MovedOutAndBackInterruptedDeletesNothing`).
            Verified by the coordinator: `go build && go vet`, `gofmt -l`,
            `go test -count=1 ./...`, `-race` on remotedelta/statedb/doctor/cmd green on
            darwin; `GOOS=linux go build/vet` clean; 60×150 fuzz fails on seeds 1, 28,
            45 only (S002). Fast check (REVIEW): nine batch orderings × every-`List`
            fault × error/crash all converge; no re-walk of this machine's own writes;
            prune cannot run on a half-walked folder; a failed `Refresh` aborts before
            planning. Its two doc points fixed here. Trade accepted and now in MANUAL
            §7: while an entering folder's walk keeps failing, every cycle aborts and
            nothing syncs (before: a silent hole). NOT healed automatically: folders
            left empty by earlier builds — `doctor --repair` (MANUAL §8 row kept for
            that). Unverified: whether real Drive ever delivers one file twice in a
            batch. Noted: `HasRemoteNode` is now test-only; one assertion in the
            moved-out-and-back test is vacuous on the interrupted cycle (the follow-up
            cycles carry the proof).
