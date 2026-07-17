# Phase 4 — Multi-machine rollout

**Goal:** second and third machines join an existing Drive folder safely via `init --adopt`.
**Exit criterion:** 3 machines, offline concurrent edit matrix tests pass.
**Status:** code + matrix tests done 2026-07-14; manual real-world 3-machine rollout pending (needs a second physical machine — the user's step).

## Tasks

### `init --adopt` — done 2026-07-14
- [x] First-merge is the **first sync over an empty baseline**, not a new planner: with no baseline rows, the existing reconcile decision table already yields upload (local-only), download (remote-only), adopt/record (same md5), conflict copy (diff md5), and — structurally — never a delete. So `init --adopt` gates then runs one `engine.Sync`.
- [x] md5-equal pairs adopted into baseline with no transfer (reconcile row `absent | new | new, same md5`).
- [x] Differing pairs → local becomes a conflict copy, remote wins the canonical name (reconcile row `absent | new | new, diff md5`).
- [x] Local-only uploaded; remote-only downloaded. **Nothing deleted, ever** — an empty baseline cannot generate a delete-class action (asserted in tests).
- [x] Plain `init` on a non-empty Drive folder without `--adopt` → hard error pointing at `--adopt`; nothing is persisted on the refusal, so an `--adopt` retry is clean. An existing *empty* folder is still reused without `--adopt`.

### Machine identity — already present (init)
- [x] `machine_name` validated (non-empty, filename-safe) in `config` and embedded in conflict filenames (`internal/conflicts`); `machine_id` (random 8-byte, stable across re-init) stored in `meta` by `initialize`.

### Multi-machine test matrix — done 2026-07-14
- [x] Harness already supports N machines (`newMachine` × N against one fake Drive; "offline" = a machine simply doesn't sync while others do).
- [x] Matrix cases: adopt union merge (`TestAdoptUnionMerge`); divergent-content conflict on adopt (`TestAdoptConflictsOnDivergentContent`); three machines with offline concurrent divergent edits converging with no lost version (`TestThreeMachineConvergence`); adopt on machine C while A/B active (`TestAdoptWhileOthersActive`). Delete-vs-edit and rename-vs-edit are covered machine-agnostically by S6/S7 in `engine_test.go`.
- [x] Invariant asserted: no content version ever lost; all machines converge to identical trees (`assertConverged`, plus an explicit "every distinct edit survives somewhere" check).

### Real-world rollout (manual) — pending (user)
- [ ] Deploy binary + `init --adopt` on second real machine; verify convergence.
- [ ] Third machine; run all three under `watch` for a day.

## Verification

- [x] Matrix tests green (2026-07-14).
- [ ] Manual 3-machine rollout done with no anomalies in `doctor` (needs physical machines).
