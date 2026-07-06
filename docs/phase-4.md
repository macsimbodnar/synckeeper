# Phase 4 — Multi-machine rollout

**Goal:** second and third machines join an existing Drive folder safely via `init --adopt`.
**Exit criterion:** 3 machines, offline concurrent edit matrix tests pass.
**Status:** not started

## Tasks

### `init --adopt`
- [ ] First-merge planner mode: non-empty Drive folder + (possibly) non-empty local dir → union of both sides.
- [ ] md5-equal pairs adopted into baseline with no transfer.
- [ ] Differing pairs (same rel_path, different md5) → local renamed to conflicted copy, remote downloaded to canonical name, conflicted copy uploaded.
- [ ] Local-only files uploaded; remote-only files downloaded. **Nothing deleted, ever, during adopt.**
- [ ] Plain `init` on a non-empty Drive folder without `--adopt` → hard error pointing at `--adopt`.

### Machine identity
- [ ] `machine_name` from config validated (non-empty, filename-safe) and embedded in conflict filenames; `machine_id` (random, stable) in `meta` distinguishes DBs with duplicate names.

### Multi-machine test matrix
- [ ] Harness extension: 3 (sync_dir, DB) pairs against one fake Drive; "offline" = machine simply doesn't sync while others do.
- [ ] Matrix cases: A edits offline while B edits same file → conflict copies on all 3 after everyone syncs; A deletes while B edits → edit survives everywhere; A renames while B edits same file; adopt on machine C while A/B active; all combined with folder moves.
- [ ] Assert invariant across every case: no content version ever lost; all machines converge to identical trees.

### Real-world rollout (manual)
- [ ] Deploy binary + `init --adopt` on second real machine; verify convergence.
- [ ] Third machine; run all three under `watch` for a day.

## Verification

- [ ] Matrix tests green.
- [ ] Manual 3-machine rollout done with no anomalies in `doctor`.
