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
done:
