id:         S008
goal:       `init` over an existing state DB with the sync folder gone empties the Drive
            folder. `cmd/synckeeper/init.go:105` creates the folder before the merge
            at `:128`, so `guards.EnsureSyncDir` never reports `recreated` and the
            baseline is never reset; the engine sees an empty folder beside a full
            baseline and trashes everything. Contradicts MANUAL §1 "`init` never
            deletes, and is safe to re-run". Found by audit 2026-11-11_adversarial (F02, Critical).
accepts:    `TestAudit20261111_F02_InitWithMissingSyncDirEmptiesDrive` passes ungated:
            3 of 3 files still on Drive and back on disk. The F5 acceptance
            (`TestF5SyncDirVanishedIsRecreatedNotPropagated`) is exercised through
            `init`'s own order of steps. MANUAL §8 row leaves the list in the fix's
            commit; testing.md row lands with the fix.
touches:    cmd/synckeeper/init.go, internal/guards, internal/engine, internal/statedb
            (only if the baseline gets bound to the folder), MANUAL.md, adocs/specs.md
            §11, adocs/testing.md
excludes:   Binding the baseline to the sync folder's identity (path or marker in meta)
            is named in the finding but is a design choice — decide with Max before
            building it; the minimal fix does not need it.
closes:     2026-11-11_adversarial-F02
paused_by:
author:     Opus subagent, coordinated by Claude (agent); started 2026-11-11 at Max's direction (DEC-002)
done:       2026-11-11. `TestAudit20261111_F02_InitWithMissingSyncDirEmptiesDrive` passes
            ungated (3 of 3 on Drive, 3 of 3 back on disk, no trash planned). `init` no
            longer creates the sync dir: new helper `initMerge` (`initialize` → the two
            announce prints → `eng.Sync{PreferNewer}`) is what RunE and the tests both
            run, so `guards.EnsureSyncDir` finds a missing folder missing, recreates it
            and resets the baseline. New tests `TestS008InitRecreatesVanishedSyncDir`
            (F5 through `init`'s own steps), `TestS008InitFirstRunCreatesSyncDir`,
            `TestS008InitDanglingSymlinkSyncDirNamesTheLink` (testing.md S008.1–S008.4).
            Verified by the coordinator: `go build && go vet`, `gofmt -l`,
            `go test -count=1 ./...` green on darwin; `GOOS=linux go build/vet` clean.
            User-visible: sync-dir errors from `init` now come from the guard, prefixed
            `merge:`, after "Initialized." and "Merging…" are printed; on a first run
            the folder appears during the merge. Fast check (REVIEW): no defect in the
            diff. Its trivial item fixed here — the F02 test now asserts the disk half
            too. Its other item is pre-existing and went to S017: a kill between the
            guard's `MkdirAll` and the baseline reset leaves an empty folder beside a
            full baseline. Not built, per `excludes`: binding the baseline to the
            folder (S017); MANUAL §8 gained S017's row in this commit.
