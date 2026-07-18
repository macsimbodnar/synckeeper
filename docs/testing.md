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
| G1 | Delete 50% of files | Blocked without `--confirm-deletes` (interactive one-shot aborts the cycle) | passing (2026-07-08, unit + scenario level) |
| G2 | Empty local dir with populated DB | Hard error | passing (2026-07-08, unit + scenario level) |
| G3 | Mass delete under the daemon (`DeferMassDelete`) | Deletes deferred, everything else synced, block surfaced in status (spec §8.1) | passing (2026-07-17) — `TestG3DaemonDefersMassDeleteButSyncsRest` (engine) |

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

Names criteria N1–N3 are spec §16.5; they live in the regression table below with the other IDs. This table holds the remaining per-platform work.

| ID | Case | Status |
|---|---|---|
| — | Windows reserved names skipped and reported | todo |
| — | Windows long paths (`\\?\` prefix) | todo |
| — | Illegal chars / trailing dots and spaces skipped | todo |
| — | Symlinks not followed, reported | todo |

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
| Activity direction: `source` round-trips (statedb v4); `activity`/`status`/`--json` show local→drive/drive→local/conflict | passing (2026-07-14, macOS) |
| Case collision: `Snapshot` keeps first-by-id + reports the rest when case-insensitive, both when sensitive | passing (2026-07-14) — `TestSnapshotCaseCollision` |
| Case-sensitivity probe agrees with a direct case-toggled stat | passing (2026-07-14) — `TestCaseInsensitiveFS` |
| Case-only local rename → remote move, not trash+upload (APFS-conditional) | passing (2026-07-14, macOS/APFS) — `TestCaseOnlyRenameBecomesMove` |

## Control socket (phase 6, stage 2)

| Case | Status |
|---|---|
| Transport round-trip, version-mismatch rejection, not-running detection (`internal/control`) | passing (2026-07-14) |
| Pause suppresses an auto-sync then resume syncs; sync-now runs even while paused (`internal/watch`, race-clean) | passing (2026-07-14) |
| `applyReload` hot-swaps poll/threshold, reports cold fields as needing restart | passing (2026-07-14) |
| End-to-end against the real daemon: ping/pause/resume/delegated-sync/reload/stopped | passing (2026-07-14, macOS) |

## Review regressions & new acceptance rows (2026-07-17, spec §16)

| ID | Case | Status |
|---|---|---|
| R1 | Remote rename to smaller path + remote edit + local edit → conflict copy survives, no data loss (was: download overwrote the moved local edit) | passing (2026-07-17) — `TestR1RemoteMoveEditVsLocalEditConflict` (engine), `TestRemoteMovePlusConflictBacksUpFromCurrentPath` (reconcile), `TestProtectedDownloadRefusedWhenBackupFails` (executor) |
| R2 | Remote same-id dir rename + new remote subdir → converges (was: mkdir created the move destination; livelock) | passing (2026-07-17) — `TestR2RemoteDirRenameWithNewSubdir` (engine), `TestDirMoveOrdersBeforeMkdirLocal` (reconcile) |
| R3 | `init --force` rebuilds the remote mirror (no silently missed remote changes) | passing (2026-07-17) — `TestR3ForceReinitSeesPriorRemoteChanges` (cmd) |
| R4 | Local edit landing between scan and download is not overwritten (requeued) | passing (2026-07-17) — `TestR4MidCycleEditBecomesConflictNotLoss` (engine), `TestR4DownloadRefusedWhenTargetDriftsMidCycle` + `TestR4DownloadRefusedWhenTargetAppearsMidCycle` (executor) |
| R5 | Read-only commands neither migrate nor break on schema mismatch | passing (2026-07-17) — `TestR5OpenReadNeverMigrates` (statedb) |
| R6 | Cross-rename swap (a↔b same cycle) converges to the correct state within bounded cycles (transient move failures accepted; no lying records) | passing (2026-07-17) — `TestR6RemoteFileSwapConverges` (engine); found + fixed silent divergence via unprotected Record |
| R7 | Remote move onto an untracked local file → the occupant is preserved as a conflict copy (backed up + uploaded), never clobbered by `MoveLocal` (adversarial analysis; was: silent local data loss reported as success) | passing (2026-07-17) — `TestR7RemoteMoveOntoUntrackedLocalFilePreserved` (engine), `TestRemoteMoveOntoUntrackedLocalFilePreserved` (reconcile), `TestR7MoveLocalRefusesUnexpectedOccupant` (executor) |
| R8 | `ConflictBackup` refuses to overwrite an existing file at its destination (crash-leftover copy) | passing (2026-07-17) — `TestR8ConflictBackupRefusesExistingDestination` (executor) |
| N1 | Case siblings collapse on case-insensitive FS (`a.txt` + `A.txt`); first by id kept, rest skipped and reported | passing (2026-07-14) — `TestSnapshotCaseCollision` (remotedelta), `TestCaseInsensitiveFS` (names), `TestCaseOnlyRenameBecomesMove` (engine, macOS/APFS) |
| N2 | Unicode-normalization siblings collapse on normalization-insensitive FS; nothing clobbered, skips reported | passing (2026-07-18) — `TestSnapshotNormalizationCollision` (remotedelta), `TestNormalizationInsensitiveFS` + `TestFoldKey` (names, probe validated on real APFS) |
| N3 | Duplicate names in one Drive folder collapse; first by id kept, rest skipped and reported | passing (2026-07-08) — covered by the `remotedelta.Snapshot` dedup suite; named case (`TestSnapshotDuplicateNameCollision`) re-scoped into W1.8.9 (2026-07-18) |

### W1.8 — adversarial round 2 (2026-07-18); all `todo`, red-first

| ID | Case | Status |
|---|---|---|
| gate | No file in `internal/executor` outside `localwrite.go` calls a raw FS-mutating stdlib function (AST walk) | todo — the enforcement half of the local-write gate, spec §7 |
| R9 | Local directory rename is one remote move: the Drive folder id is unchanged and the plan contains no delete-class action (was: `mkdir_remote` + `trash_remote`, id churned). Also pins the decided pairing rule: a **nested** tree rename collapses to one move; an **empty** directory pairs to nothing and stays delete + create (intended, not a bug); a **scatter** (children landing in two places) must not collapse; a directory row survives the move as `is_dir = 1`; **concurrent remote changes under the renamed dir** (a child added or edited from another machine) download under the **new** name — no zombie source dir, no duplicate upload (2026-07-18, plan review) | todo — reconcile + engine |
| R10 | Renaming a folder (and a folder tree) neither trips the mass-delete guard nor leaves the daemon in a standing block (was: 21 deletes / 41 items → one-shot aborted, daemon wedged permanently) | todo — guards unit + engine + daemon; acceptance G4 |
| R11 | New local file inside a remotely-moved directory → conflict copy per spec §4.2, exactly one action per rel_path (was: no conflict copy, `upload` + `download` on one path) | todo — reconcile + engine |
| R12 | The plan never emits two **transfer-stage** actions on one rel_path or an ancestor/descendant pair; a plan that does is refused, not raced (spec §4.5's concurrency rule made executable; serial stages sequence overlapping paths by design — scoped 2026-07-18, plan review) | todo — reconcile property test + executor |
| R13 | `QuarantineLocal` refuses a source that drifted since the scan — an edit landing mid-cycle wins the cycle instead of being quarantined ("edit beats delete", §4.2) | todo — executor + engine; the gate's first customer |
| R14 | `reload` under sustained fsnotify event load is race-clean (must generate event load — the current suite is clean only because no test reloads while events flow) | todo — `internal/watch`, `-race` |
| R15 | A watcher rebuild failure degrades to polling and the daemon survives (was: `return err` killed it) | todo — `internal/watch` |
| R16 | A crashed directory move (renamed on disk, DB uncommitted) does not plan a remote trash for the empty dir | todo — reconcile + engine |
| R17 | `remotedelta.Snapshot` terminates on a cyclic parent chain in the cache | todo — remotedelta |

### Deferred acceptance

| ID | Case | Status |
|---|---|---|
| FZ1 | Seeded random-ops fuzzer, N machines + crash points: convergence, no content loss, deterministic replay, **identity stability**, **§4.5 structural invariant** (oracle strengthened 2026-07-18) | todo — W4, now ahead of W3 |
| W1-scale | ≥50k files under the daemon: no fd exhaustion; watcher kill → polling → recovery | todo — W3 |

## Live smoke (any phase, manual)

| Case | Status |
|---|---|
| `SYNCKEEPER_LIVE_TEST=1` round-trip against real Drive (upload/download/trash/changes) | passing (2026-07-08, macOS) |
