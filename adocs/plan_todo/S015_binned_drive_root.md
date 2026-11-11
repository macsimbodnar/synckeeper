id:         S015
goal:       `watch` and `sync` never check the Drive root: only `init.go:245` and
            `doctor.go:193` call `root.Resolve`, while spec §12 says a root that is
            gone or in Drive's bin is recreated. Against the fake, a binned root is
            synced into with clean success. Found by audit 2026-11-11_adversarial (F10, Medium, partly
            unconfirmed).
accepts:    First, the live suite records what the real change feed emits for a binned
            root's descendants — the finding is confirmed or corrected on that
            evidence. Then `TestAudit20261111_F10_BinnedDriveRootIsSyncedIntoSilently`
            passes ungated: a gone/binned root goes through `root.Resolve` or stops
            the cycle with a visible error. Spec §12 and the code agree. MANUAL §8 row
            leaves the list in the fix's commit; testing.md row lands with the fix.
touches:    cmd/synckeeper/watch.go, cmd/synckeeper/sync.go, internal/root,
            internal/remotedelta, internal/driveclient (fake fidelity), MANUAL.md,
            adocs/specs.md §12, adocs/testing.md
excludes:   Nothing is built before the live behaviour is recorded.
closes:     2026-11-11_adversarial-F10
paused_by:
author:
done:
