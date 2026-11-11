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
done:       2026-11-11. `TestAudit20261111_F04_UpdateRemoteOverwritesConcurrentRemoteEdit`
            passes ungated, unchanged. The planner pins the Drive state each remote
            mutation was planned on (`Action.RemoteWant`, `pinRemote`, six emission
            sites: parent id, name, type, a file's md5); `internal/executor/remotewrite.go`
            is the one choke point (AST test) and hands it to the client through the
            context (`driveclient.WithPrecondition`); `Update`/`Trash`/`Move` re-read the
            item and refuse with `ErrDrift` — a failed action, replanned next cycle as
            conflict or edit-beats-delete. No schema change; journal field additive.
            New tests: 5 `TestRemoteGate*` (trash, move, update, folder move racing a
            remote write; an edit inside the move window), `TestPrecondition*`,
            `TestRemoteMutationsPinTheirPlannedDriveState`,
            `TestGateNoDriveMutationOutsideRemotewrite` (testing.md S010.1–S010.10).
            Deviation from the suggested shape, accepted: the check lives in the client,
            not as an executor-side `Get`, so the F04 test's hook (which fires at the
            `Update` call) passes unedited. Verified by the coordinator: `go build &&
            go vet`, `gofmt -l`, `go test -count=1 ./...`, `-race` on executor/engine/
            reconcile/driveclient green on darwin; `GOOS=linux go build/vet` clean; the
            60×150 fuzz sweep fails on seeds 1, 28, 45 with this change and on an
            untouched copy of `06df839` alike (all the §4.5 wedge, S002).
            NOT verified: the real client against real Drive (no live run).
            Fast check (REVIEW): no permanent false refusal, no bypass, the md5 kept on
            a move is sound. Its doc item fixed here — the residual window was called
            "one round trip"; it is the file's whole upload for an `Update` and the
            retry backoff for a `Trash`/`Move` (MANUAL §6, specs §7, real.go comment).
            Recorded, not fixed: `remotewant_test.go` has no case for the fold
            step-aside site (plan.go:514; pinned in code). Follow-ups: S019 (real-client
            gate: live verification, narrower window, fake fidelity for trashed
            descendants), S020 (folder deleted here vs renamed there — pre-existing,
            reproduced), S021 (a folder trash does not look inside the folder — the
            part of F04's class this step leaves open; MANUAL §8 keeps that row).
