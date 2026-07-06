# Synckeeper — Test Matrix

Tracks every named test from the spec. Update the status column as tests are implemented and pass. Approach: `go test ./...`; `driveclient` is an interface with an in-memory fake (ids, versions, md5, trash, changes feed); `reconcile` gets table-driven tests over the full decision matrix; fault injection via executor checkpoints; live smoke suite only under `SYNCKEEPER_LIVE_TEST=1` against a throwaway Drive folder.

Statuses: `todo` / `written` / `passing` / `passing (date, platform)`.

## Reconcile decision-table tests (phase 1)

One table-driven case per decision-table row (13 rows) plus edge cases (rel_path joins, move pairing, directory ordering, delete-vs-edit orders). Tracked as a single unit:

| Suite | Status |
|---|---|
| `internal/reconcile` decision matrix | todo |

## Scenario tests (phase 1)

| ID | Scenario | Status |
|---|---|---|
| S1 | Create local → appears remote and on machine B (second DB + sync dir against same fake drive) | todo |
| S2 | Create remote → appears local | todo |
| S3 | Edit local / edit remote sequentially, both directions | todo |
| S4 | Concurrent divergent edit → conflicted copy on all machines, no version lost | todo |
| S5 | Delete local → trashed remote; delete remote → local quarantined | todo |
| S6 | Delete vs edit, both orders → edited version survives | todo |
| S7 | Rename/move local and remote, including move out of tree | todo |
| S8 | Nested folders, deep tree, empty folders | todo |

## Fault tests (phase 2) — kill at checkpoint, then rerun sync

| ID | Fault | Expected | Status |
|---|---|---|---|
| F1 | Crash after upload, before DB commit | No duplicate, row adopted | todo |
| F2 | Crash mid-download temp file | Temp cleaned, retried, target never half-written | todo |
| F3 | Crash between rename and DB commit | Repaired via pending_ops replay | todo |
| F4 | DB deleted entirely | `doctor --repair` rebuilds baseline by md5 match, no deletes propagated | todo |
| F5 | Sync dir unmounted | Hard error, remote untouched | todo |

## Guard tests (phase 2)

| ID | Guard | Expected | Status |
|---|---|---|---|
| G1 | Delete 50% of files | Blocked without `--confirm-deletes` | todo |
| G2 | Empty local dir with populated DB | Hard error | todo |

## Multi-machine matrix (phase 4)

| Case | Status |
|---|---|
| Offline concurrent edit, same file, 3 machines → conflicts everywhere | todo |
| Delete vs edit across machines → edit survives everywhere | todo |
| Rename vs edit of same file | todo |
| `init --adopt` on machine C while A/B active | todo |
| Combined with folder moves | todo |

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
| 2-hour random-edit soak, both sides, no divergence | todo |

## Live smoke (any phase, manual)

| Case | Status |
|---|---|
| `SYNCKEEPER_LIVE_TEST=1` round-trip against real Drive (upload/download/trash/changes) | todo |
