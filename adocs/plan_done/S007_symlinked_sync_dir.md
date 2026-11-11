id:         S007
goal:       A `sync_dir` that is a symlink is scanned as empty (`filepath.WalkDir` does
            not follow a symlinked root, `internal/scanner/scanner.go:24`) while
            `guards.EnsureSyncDir` follows it and passes. Empty snapshot + full
            baseline = the whole Drive folder trashed and every other machine binned,
            with both cycles reporting success. Found by audit 2026-11-11_adversarial (F01, Critical).
accepts:    `TestAudit20261111_F01_SymlinkedSyncDirEmptiesDrive` passes ungated (its
            `SYNCKEEPER_AUDIT_REPRO` gate removed). Scanner + engine tests with a
            symlinked root: files under it are scanned, uploaded and downloaded. A scan
            returning zero entries while `os.ReadDir(root)` is non-empty is a hard,
            visible error. MANUAL §8 row leaves the list in the fix's commit;
            testing.md row lands with the fix.
touches:    internal/scanner, internal/config (or wherever the root is resolved),
            internal/guards, internal/engine, MANUAL.md, adocs/specs.md, adocs/testing.md
excludes:   Symlinks *inside* the tree (S013). Guarding an emptied-but-real sync dir —
            a recorded owner decision (W18-D, W14); reopening it is Max's call.
closes:     2026-11-11_adversarial-F01
paused_by:
author:     Opus subagent, coordinated by Claude (agent); started 2026-11-11 at Max's direction (DEC-002)
done:       2026-11-11. `TestAudit20261111_F01_SymlinkedSyncDirEmptiesDrive` passes ungated;
            new red-first tests in `symlinkroot_test.go` under internal/scanner, engine,
            watch, doctor (testing.md S007.1–S007.7). The engine resolves `sync_dir` once
            per cycle and scans/writes/bins under the resolved path; `scanner.Scan`, the
            watch backends and `doctor` resolve too; a dangling link is a hard error; a
            scan reaching no entry over a non-empty dir is a hard error. Verified by the
            coordinator: `go build && go vet`, `gofmt -l`, `go test -count=1 ./...` green
            on darwin; `GOOS=linux go build/vet` clean. Not run on Linux hardware.
            Fast check (REVIEW): no way found for the fix to lose or misplace a file.
            Its one trivial item fixed here — MANUAL §6 now says `init` on a dangling
            link fails with the older `mkdir …: file exists` message. Its one real item
            became S017: a *retargeted* link (or an edited `sync_dir`) is scanned
            against the old baseline. Known and accepted: the empty-scan error can fire
            for one cycle if a file lands in an empty root mid-scan; engine paths in
            logs are now the resolved ones (`/private/var…` on macOS); `EvalSymlinks`
            on Windows network/mapped drives is unchecked (S006).
