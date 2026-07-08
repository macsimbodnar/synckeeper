# Phase 2 — Safety hardening

**Goal:** the engine survives crashes at any point and refuses to amplify mistakes (mass deletes, unmounted dirs, lost DB).
**Exit criterion:** fault tests F1–F5 pass (see [testing.md](testing.md)).
**Status:** done 2026-07-08 — F1–F5 and scenario-level G1–G2 green; doctor verified against real Drive

## Tasks

### Guards (`internal/guards`)
- [x] Mass-delete guard: planned batch trashing/quarantining > `mass_delete_threshold` of tracked files AND > 10 absolute → abort with a report listing the deletions; proceed only with `sync --confirm-deletes`.
- [x] Sync-dir guard: missing, unreadable, or empty-while-DB-nonempty → hard error before any plan executes. Never interpret as "everything was deleted".
- [x] Guard tests G1, G2.

### Quarantine lifecycle
- [x] Retention purge: entries older than `quarantine_retention_days` removed at the end of a successful sync only.
- [x] `status` reports quarantine entry count and total size.

### Delete-vs-edit and duplicate handling (verify, then close gaps)
- [x] Confirm reconcile handles delete-vs-edit both orders (S6) including folder-level cases.
- [x] Duplicate remote names: deterministic keep-first-by-id, rest reported; test with 3+ duplicates and with a duplicate appearing via changes feed.

### Crash resumability
- [x] Fault-injection hook in executor: named checkpoints (`upload_before_commit`, `download_temp_written`, `download_before_commit`) armed via the `executor.FaultHook` test hook.
  - Deviation: the hook aborts the op with an error instead of panicking — a panic in a transfer-pool goroutine would kill the test process. State-wise this equals a crash at the checkpoint minus deferred temp cleanup, which the planted-orphan-temp test covers separately; a real `kill -9` is exercised manually (see verification).
- [x] Orphan temp-file cleanup on startup (`.synckeeper.tmp.*` older than the current run).
- [x] Fault tests F1–F3, F5: kill at checkpoint → rerun sync → verify repaired state (no duplicates, no half-written targets, journal replayed).

### `synckeeper doctor`
- [x] Read-only cross-check: DB vs disk vs Drive; report rows without files, files without rows, md5 mismatches, orphan pending_ops, orphan temps.
- [x] `--repair`: rebuild baseline by matching md5s (safe direction only — never propagates deletes), clean orphan temps, resolve stale pending_ops.
- [x] F4: delete DB entirely → `doctor --repair` rebuilds baseline, next sync propagates no deletes and re-transfers nothing that matches by md5.

## Verification

- [x] F1–F5 green, G1–G2 green, S1–S8 still green.
- [x] Manual: `kill -9` a real sync mid-transfer, rerun, `doctor` reports clean. (Done 2026-07-08 with a 40 MB upload killed after 4 s: stale op discarded, replanned, no duplicate on Drive, doctor clean.)
