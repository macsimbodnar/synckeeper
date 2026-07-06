# Phase 2 — Safety hardening

**Goal:** the engine survives crashes at any point and refuses to amplify mistakes (mass deletes, unmounted dirs, lost DB).
**Exit criterion:** fault tests F1–F5 pass (see [testing.md](testing.md)).
**Status:** not started

## Tasks

### Guards (`internal/guards`)
- [ ] Mass-delete guard: planned batch trashing/quarantining > `mass_delete_threshold` of tracked files AND > 10 absolute → abort with a report listing the deletions; proceed only with `sync --confirm-deletes`.
- [ ] Sync-dir guard: missing, unreadable, or empty-while-DB-nonempty → hard error before any plan executes. Never interpret as "everything was deleted".
- [ ] Guard tests G1, G2.

### Quarantine lifecycle
- [ ] Retention purge: entries older than `quarantine_retention_days` removed at the end of a successful sync only.
- [ ] `status` reports quarantine entry count and total size.

### Delete-vs-edit and duplicate handling (verify, then close gaps)
- [ ] Confirm reconcile handles delete-vs-edit both orders (S6) including folder-level cases.
- [ ] Duplicate remote names: deterministic keep-first-by-id, rest reported; test with 3+ duplicates and with a duplicate appearing via changes feed.

### Crash resumability
- [ ] Fault-injection hook in executor: named checkpoints (`after_upload_before_commit`, `mid_download`, `after_rename_before_commit`, `mid_db_commit`, ...) that panic/exit when armed via env var or test hook.
- [ ] Orphan temp-file cleanup on startup (`.synckeeper.tmp.*` older than the current run).
- [ ] Fault tests F1–F3, F5: kill at checkpoint → rerun sync → verify repaired state (no duplicates, no half-written targets, journal replayed).

### `synckeeper doctor`
- [ ] Read-only cross-check: DB vs disk vs Drive; report rows without files, files without rows, md5 mismatches, orphan pending_ops, orphan temps.
- [ ] `--repair`: rebuild baseline by matching md5s (safe direction only — never propagates deletes), clean orphan temps, resolve stale pending_ops.
- [ ] F4: delete DB entirely → `doctor --repair` rebuilds baseline, next sync propagates no deletes and re-transfers nothing that matches by md5.

## Verification

- [ ] F1–F5 green, G1–G2 green, S1–S8 still green.
- [ ] Manual: `kill -9` a real sync mid-transfer, rerun, `doctor` reports clean.
