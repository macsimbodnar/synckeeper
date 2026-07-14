# Synckeeper — Test Matrix

Tracks every named test from the spec. Update the status column as tests are implemented and pass. Approach: `go test ./...`; `driveclient` is an interface with an in-memory fake (ids, versions, md5, trash, changes feed); `reconcile` gets table-driven tests over the full decision matrix; fault injection via executor checkpoints; live smoke suite only under `SYNCKEEPER_LIVE_TEST=1` against a throwaway Drive folder.

Statuses: `todo` / `written` / `passing` / `passing (date, platform)`.

## Reconcile decision-table tests (phase 1)

One table-driven case per decision-table row (13 rows) plus edge cases (rel_path joins, move pairing, directory ordering, delete-vs-edit orders). Tracked as a single unit:

| Suite | Status |
|---|---|
| `internal/reconcile` decision matrix | passing (2026-07-08) |

## Scenario tests (phase 1)

| ID | Scenario | Status |
|---|---|---|
| S1 | Create local → appears remote and on machine B (second DB + sync dir against same fake drive) | passing (2026-07-08) |
| S2 | Create remote → appears local | passing (2026-07-08) |
| S3 | Edit local / edit remote sequentially, both directions | passing (2026-07-08) |
| S4 | Concurrent divergent edit → conflicted copy on all machines, no version lost | passing (2026-07-08) |
| S5 | Delete local → trashed remote; delete remote → local quarantined | passing (2026-07-08) |
| S6 | Delete vs edit, both orders → edited version survives | passing (2026-07-08) |
| S7 | Rename/move local and remote, including move out of tree | passing (2026-07-08) |
| S8 | Nested folders, deep tree, empty folders | passing (2026-07-08) |

## Fault tests (phase 2) — kill at checkpoint, then rerun sync

| ID | Fault | Expected | Status |
|---|---|---|---|
| F1 | Crash after upload, before DB commit | No duplicate, row adopted | passing (2026-07-08) |
| F2 | Crash mid-download temp file | Temp cleaned, retried, target never half-written | passing (2026-07-08) |
| F3 | Crash between rename and DB commit | Repaired via pending_ops replay | passing (2026-07-08) |
| F4 | DB deleted entirely | `doctor --repair` rebuilds baseline by md5 match, no deletes propagated | passing (2026-07-08) |
| F5 | Sync dir unmounted | Hard error, remote untouched | passing (2026-07-08) |

## Guard tests (phase 2)

| ID | Guard | Expected | Status |
|---|---|---|---|
| G1 | Delete 50% of files | Blocked without `--confirm-deletes` | passing (2026-07-08, unit + scenario level) |
| G2 | Empty local dir with populated DB | Hard error | passing (2026-07-08, unit + scenario level) |

## Multi-machine matrix (phase 4)

| Case | Status |
|---|---|
| `init --adopt` union merge (local-only up, remote-only down, same-md5 adopt, nothing deleted) | passing (2026-07-14) — `TestAdoptUnionMerge` |
| `init --adopt` divergent content → conflict copy, remote wins canonical | passing (2026-07-14) — `TestAdoptConflictsOnDivergentContent` |
| Non-empty Drive folder without `--adopt` → hard error, nothing persisted | passing (2026-07-14) — `TestInitializeRefusesNonEmptyWithoutAdopt` |
| Offline concurrent edit, same file, 3 machines → converge, no version lost | passing (2026-07-14) — `TestThreeMachineConvergence` |
| `init --adopt` on machine C while A/B active | passing (2026-07-14) — `TestAdoptWhileOthersActive` |
| Delete vs edit across machines → edit survives everywhere | passing (2026-07-08) — S6 (machine-agnostic) |
| Rename vs edit of same file | passing (2026-07-08) — S7 (machine-agnostic) |
| Manual real 3-machine rollout, a day under `watch`, clean `doctor` | todo (needs physical machines) |

## Platform tests (phase 5)

| Case | Status |
|---|---|
| Case collision (Drive has `a.txt` + `A.txt`, case-insensitive local FS) | todo |
| Windows reserved names skipped and reported | todo |
| Windows long paths (`\\?\` prefix) | todo |
| Illegal chars / trailing dots and spaces skipped | todo |
| Symlinks not followed, reported | todo |

## Soak (phase 3)

| Case | Status |
|---|---|
| 2-hour random-edit soak, both sides, no divergence | passing (2026-07-09, macOS) — converged on 10,858 files; late-stage fd exhaustion degraded to polling as designed and still converged (see decisions.md 2026-07-12) |
| Cache-prune + polling-latch regression tests (`internal/remotedelta`, `internal/watch`) + 90 s soak re-run | passing (2026-07-12) |

## Monitoring (phase 6, stage 1)

| Case | Status |
|---|---|
| statedb v3: daemon_status round-trip; activity ring capped at 500, newest-first, limit honored | passing (2026-07-14) |
| Running watcher records daemon_status (running/mode/pid/heartbeat/last-cycle) and derives per-action activity | passing (2026-07-14) |
| Clean shutdown flips recorded state to stopped | passing (2026-07-14) |
| `service status` parsers (launchctl PID / systemctl is-enabled+is-active / schtasks Status) on canned output | passing (2026-07-14) |
| CLI render smoke: `status` never-run / running / stale, `--json`, `activity`, `config`, `account` | passing (2026-07-14, macOS) |

## Control socket (phase 6, stage 2)

| Case | Status |
|---|---|
| Transport round-trip, version-mismatch rejection, not-running detection (`internal/control`) | passing (2026-07-14) |
| Pause suppresses an auto-sync then resume syncs; sync-now runs even while paused (`internal/watch`, race-clean) | passing (2026-07-14) |
| `applyReload` hot-swaps poll/threshold, reports cold fields as needing restart | passing (2026-07-14) |
| End-to-end against the real daemon: ping/pause/resume/delegated-sync/reload/stopped | passing (2026-07-14, macOS) |

## Live smoke (any phase, manual)

| Case | Status |
|---|---|
| `SYNCKEEPER_LIVE_TEST=1` round-trip against real Drive (upload/download/trash/changes) | passing (2026-07-08, macOS) |
