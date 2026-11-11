id:         S009
goal:       An interrupted mirror rebuild is read as a mass deletion on Drive.
            `ForceFullWalk` (`internal/remotedelta/remotedelta.go:52-67`) clears the
            mirror and refills it non-atomically with `MetaWalkDone` still set; a
            dropped connection or Ctrl-C leaves a partial mirror the next cycle trusts,
            and baseline rows missing from it plan local deletes. Reached from an
            `init` re-run (`init.go:259`) and `doctor --repair` (`doctor.go:210`).
            Found by audit 2026-11-11_adversarial (F03, High).
accepts:    `TestAudit20261111_F03_InterruptedMirrorRebuildBinsLocalFiles` passes
            ungated. A fault injected at every `List` of the rebuild plans zero local
            deletes on the following cycle. MANUAL §8 row leaves the list in the fix's
            commit; testing.md row lands with the fix.
touches:    internal/remotedelta, internal/statedb, internal/doctor,
            cmd/synckeeper/init.go, MANUAL.md, adocs/specs.md §12, adocs/testing.md
excludes:   Retrying network-level errors in `internal/driveclient/retry.go` — related,
            separate.
closes:     2026-11-11_adversarial-F03
paused_by:
author:     Opus subagent, coordinated by Claude (agent); started 2026-11-11 at Max's direction (DEC-002)
done:       2026-11-11. `TestAudit20261111_F03_InterruptedMirrorRebuildBinsLocalFiles`
            passes ungated. `ForceFullWalk` empties `remote_nodes` and retracts
            `initial_walk_done` in one transaction (`ClearRemoteNodesTx` +
            `DeleteMetaTx`); the marker returns only after the walk and the page token
            are stored, so an interrupted rebuild makes the next `Refresh` walk in full.
            No schema change. `TestS009InitRebuildInterruptedAtEveryList` and
            `TestS009RepairRebuildInterruptedAtEveryList`: a fault at each of the 5
            `List` calls, as an error and as a crash image of the committed DB, 20 cases,
            each planning zero actions (testing.md S009.1–S009.3). Verified by the
            coordinator: `go build && go vet`, `gofmt -l`, `go test -count=1 ./...`,
            `-race` on remotedelta/statedb/doctor/cmd green on darwin; `GOOS=linux go
            build/vet` clean. Fast check (REVIEW): no deletion or corruption hole at any
            interruption point, in any token state. Its doc item fixed here — MANUAL §7
            and specs §12 now state the no-stored-token case (first `init` or lost DB,
            interrupted: cycles fail with "no changes page token stored" until `init`
            completes; pre-existing, nothing deleted). Untested, safe by reading only:
            that no-token case, a crash between the token write and the marker write,
            a Drive change inside the interrupted window. Found out of scope and
            reproduced by the fast check: S018 (a moved-in Drive folder whose walk fails
            once is never walked again); MANUAL §8 gained its row in this commit.
