# Phase 1 — One-shot bidirectional sync

**Goal:** `synckeeper sync` performs a full, safe, three-way sync. After this phase the tool is usable daily via cron/Task Scheduler and functionally replaces Dropbox.
**Exit criterion:** scenario tests S1–S8 pass (see [testing.md](testing.md)).
**Status:** done 2026-07-08 — S1–S8 green against the fake, live smoke green against real Drive, manual round-trip verified

## Build order

Reconcile is pure and test-first; everything else feeds it or executes its output.

### Names (`internal/names`)
- [x] rel_path (posix-style) ↔ Drive name/parent mapping.
- [x] Ignore-pattern matching against `engine.ignore` globs.
- [x] Detection (not yet full handling) of invalid-on-some-platform names and Google-native mimeTypes → "skipped" report items.

### Local scanner (`internal/scanner`)
- [x] Full recursive walk of sync_dir → local snapshot: rel_path, is_dir, size, mtime_ns.
- [x] md5 computed lazily: only when size or mtime_ns differs from the baseline row; otherwise trust baseline.
- [x] Symlinks: not followed, reported.
- [x] Ignore patterns applied during walk; `.synckeeper.tmp.*` always ignored.

### Remote delta (`internal/remotedelta`)
- [x] Initial full walk: paginated `Files.List` per folder from root_folder_id → remote snapshot + parent map persisted in DB.
- [x] `Changes.List(pageToken, includeRemoved=true)` consumption → snapshot updates; tree-membership via parent map (moved out of tree = delete, moved in = create).
- [x] Google-native mimeTypes filtered out and reported.
- [x] Duplicate names in one folder: keep first by id, report the rest; never download duplicates.
- [x] New page token persisted only after the batch is fully processed.

### Reconcile (`internal/reconcile`) — pure, test-first
- [x] Types: `Item` snapshots, `Action` variants (upload, download, adopt, conflict, trash_remote, quarantine_local, mkdir_remote, mkdir_local, move_remote, move_local, record).
- [x] Table-driven tests written FIRST, one case per row of the spec decision table (13 rows), plus edge cases: nil baseline join on rel_path, same-md5 races, delete-vs-edit both orders.
- [x] Implement the decision table exactly as specified.
- [x] Local move detection: pair scanner delete+create with identical md5+size → `move_remote`; unpaired stay delete+create.
- [x] Remote move handling: same drive_file_id with new name/parent → `move_local`.
- [x] Directory rules: mkdirs top-down before children; dirs trashed only when empty after children resolved; delete-folder vs add-file-inside → resurrect folder.
- [x] Deterministic action ordering: mkdirs (top-down), moves, transfers, deletes (bottom-up).

### Executor (`internal/executor`)
- [x] All planned actions written to `pending_ops` (state `planned`) before any execution.
- [x] Atomic download protocol per spec: temp file in destination dir, streaming md5, verify against Drive md5Checksum, fsync, rename, fsync parent dir (POSIX), stat, commit `items` row + op `done` in one transaction.
- [x] Upload protocol: pre-hash; resumable chunked (`googleapi.ChunkSize(8MB)`) for > 5 MB, simple otherwise; commit only after Drive returns version + matching md5Checksum; if local mtime changed during upload → mark dirty, requeue.
- [x] Trash remote = `Files.Update(trashed=true)`; never `Files.Delete`.
- [x] Quarantine local = move to `<config_dir>/quarantine/<YYYY-MM-DD>/<rel_path>` (structure preserved).
- [x] Startup replay: re-verify and resume any non-`done` pending_ops idempotently (check current disk/Drive state before acting).
  - Deviation: implemented as discard-and-replan — stale ops are dropped, orphan temp files removed, and a fresh plan is generated; partial effects (e.g. uploaded-but-uncommitted files) self-heal through the decision table (both-new-same-md5 → adopt). Fault tests in phase 2 must prove F1–F3 hold under this model.
- [x] Worker pool (4 transfers) with path-conflict serialization: no two concurrent actions on the same rel_path or ancestor/descendant paths; plan generation and DB commits single-threaded.

### Conflicts (`internal/conflicts`)
- [x] Naming: `<stem> (conflict <machine_name> <YYYY-MM-DD_HHMMSS>)<suffix>`.
- [x] Materialization: local becomes conflicted copy, remote wins canonical name; conflicted copy is uploaded too.

### `synckeeper sync` command
- [x] Pipeline: lock → guards → pull remote delta → scan local → reconcile → execute → report summary. (Page token persists with the remote-nodes batch, before reconcile — replaying a batch is idempotent, so this satisfies the "only after fully processed" invariant.)
  - Ahead of schedule: the mass-delete guard (G1) and empty-dir guard (G2) landed now rather than phase 2 — the window between "deletes work" and "guards exist" was too dangerous to ship even to myself.
- [x] `--dry-run`: print the plan, execute nothing, change nothing.
- [x] `--confirm-deletes` flag accepted (guard enforcement in phase 2).
- [x] Non-zero exit code when any op failed and remains pending.

### Test infrastructure
- [x] In-memory fake Drive completing full semantics: ids, versions, md5, parents, trash, changes feed with page tokens. Shared by all scenario tests.
- [x] Scenario harness: N (sync_dir, state DB) pairs against one fake Drive to simulate machines.
- [x] Scenario tests S1–S8 (definitions in [testing.md](testing.md)).
- [x] Live smoke suite gated on `SYNCKEEPER_LIVE_TEST=1` against a throwaway Drive folder: upload/download/trash/changes round-trip, validating the fake's semantics against real Drive.

## Verification

- [x] S1–S8 green against the fake.
- [x] Live smoke green at least once against real Drive.
- [x] Manual: run `sync` twice on a real folder; second run is a no-op; edits in both directions propagate; a concurrent divergent edit yields a conflicted copy visible on both sides.
