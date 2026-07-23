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
| 2-hour random-edit soak, both sides, no divergence | passing (2026-07-09, macOS, fsnotify) — converged on 10,858 files; late-stage fd exhaustion degraded to polling as designed and still converged (see decisions.md 2026-07-12) |
| Cache-prune + polling-latch regression tests (`internal/remotedelta`, `internal/watch`) + 90 s soak re-run | passing (2026-07-12) |
| Soak re-run on the FSEvents backend (W3.5) | **wired + smoked (2026-07-23, macOS)** — `TestSoak` now runs against the production backend (FSEvents on darwin+cgo); 30 s and 90 s smokes converged (89 / more files) with clean `doctor` on both machines. The full 2-hour gate (`SYNCKEEPER_SOAK_SECONDS=7200`) is the release ritual, run by Max |

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
| N3 | Duplicate names in one Drive folder collapse; first by id kept, rest skipped and reported | passing (2026-07-08; named case 2026-07-19) — `TestSnapshotDuplicateNameCollision` (`internal/remotedelta`: first by id kept, skip reported) plus the dedup suite |

### W1.8 — adversarial round 2 (planned 2026-07-18); landed red-first, all rows passing (2026-07-19)

| ID | Case | Status |
|---|---|---|
| gate | No file in `internal/executor` outside `localwrite.go` calls a raw FS-mutating stdlib function (AST walk; test files excluded — the gate governs production mutations) | passing (2026-07-18) — `TestGateNoRawFSMutationOutsideLocalwrite` (executor); verified to catch a planted violation |
| R9 | Local directory rename is one remote move: the Drive folder id is unchanged and the plan contains no delete-class action (was: `mkdir_remote` + `trash_remote`, id churned). Also pins the decided pairing rule: a **nested** tree rename collapses to one move; an **empty** directory pairs to nothing and stays delete + create (intended, not a bug); a **scatter** (children landing in two places) must not collapse; a directory row survives the move as `is_dir = 1`; **concurrent remote changes under the renamed dir** (a child added or edited from another machine) download under the **new** name — no zombie source dir, no duplicate upload (2026-07-18, plan review) | passing (2026-07-18) — reconcile: `TestR9LocalDirRenameIsOneRemoteMove`, `TestR9NestedTreeRenameCollapsesToOneMove`, `TestR9EmptyDirRenameStaysDeleteCreate`, `TestR9ScatterDoesNotCollapse`, `TestR9RemoteEditUnderLocallyRenamedDir`, `TestR9RemoteNewChildUnderLocallyRenamedDir`; engine: `TestR9LocalDirRenameKeepsFolderIdentity` (two machines, id + `is_dir` pinned), `TestR9ConcurrentRemoteAddUnderRenamedDir` |
| R10 | Renaming a folder (and a folder tree) neither trips the mass-delete guard nor leaves the daemon in a standing block (was: 21 deletes / 41 items → one-shot aborted, daemon wedged permanently) | passing (2026-07-18) — `TestR10GuardCountsContentNotContainers` (guards unit: dir-only never trips, file deletions still trip regardless of container count), `TestR10EmptyFolderTreeRenameDoesNotTripGuard` (engine one-shot), `TestR10EmptyFolderTreeRenameDoesNotWedgeDaemon` (engine, `DeferMassDelete`); acceptance G4 |
| R11 | New local file inside a remotely-moved directory → conflict copy per spec §4.2, exactly one action per rel_path (was: no conflict copy, `upload` + `download` on one path) | passing (2026-07-18) — `TestR11NewLocalFileUnderRemotelyMovedDirConflicts` + `...Adopts` (reconcile), `TestR11NewLocalFileUnderRemotelyMovedDirBecomesConflict` (engine: both versions survive, exactly one x.txt on Drive) |
| R12 | The plan never emits two **transfer-stage** actions on one rel_path or an ancestor/descendant pair; a plan that does is refused, not raced (spec §4.5's concurrency rule made executable; serial stages sequence overlapping paths by design — scoped 2026-07-18, plan review) | passing (2026-07-19) — `reconcile.ValidateTransferStage` + `TestR12Validate*` (incl. the legit dir-Record + child exemption), `TestR12ApplyRefusesOverlappingTransferStage` (executor: refused before anything journals), **property net**: `runPlan` validates every reconcile scenario in the suite |
| R13 | `QuarantineLocal` refuses a source that drifted since the scan — an edit landing mid-cycle wins the cycle instead of being quarantined ("edit beats delete", §4.2) | passing (2026-07-18) — `TestR13QuarantineLocalRefusesDriftedSource` + `TestR13QuarantineLocalProceedsWhenPinMatches` (executor), `TestR13MidCycleEditWinsOverQuarantine` (engine, resurrects to Drive next cycle) |
| R14 | `reload` under sustained fsnotify event load is race-clean (must generate event load — the current suite is clean only because no test reloads while events flow) | passing (2026-07-19) — `TestR14ReloadUnderEventLoadIsRaceClean` (`internal/watch`, meaningful only under `-race`: 2 ms write storm during 25 reloads, then a post-storm write still syncs; pre-fix code raced applyReload-write vs pump-read as predicted) |
| R15 | A watcher rebuild failure degrades to polling and the daemon survives (was: `return err` killed it) | passing (2026-07-19) — `TestR15WatcherRebuildFailureDegradesToPolling` (injected creation failure at the rebuild: polling-only mode recorded, a write still syncs, watching restored by the cadence retry), `TestR15StartupWatcherFailureStartsPollingOnly` (same shape at launch — scope extended, decisions.md "W1.8.7") |
| R16 | A crashed directory move (renamed on disk, DB uncommitted) does not plan a remote trash for the empty dir (red test showed the fuller defect: a gate-refused `MoveLocal` error-loop + the trash "converging" destructively) | passing (2026-07-19) — `TestR16CrashedDirMoveDoesNotTrashRemote` + `TestR16LocallyDeletedDirStillTrashes` (reconcile: the guard never swallows a real deletion), `TestR16CrashedDirMoveKeepsRemoteFolder` (engine: folder id survives on Drive, row adopted at n, second cycle idle) |
| R17 | `remotedelta.Snapshot` terminates on a cyclic parent chain in the cache | passing (2026-07-19) — `TestR17SnapshotTerminatesOnParentCycle` (the root-as-own-child row, the one cycle reachable under the single-parent schema; visited set mirrors `prune`'s) |
| R18 | A local rename's `MoveRemote` commit never restates local truth: an edit landing between scan and commit stays visibly dirty and uploads next cycle (was: the commit stamped the scanned md5 onto the edited file's stat → silent permanent divergence, the R6 class; reproduced 2026-07-18, round 3) | passing (2026-07-18) — `TestR18MoveRemoteCommitKeepsLocalTruth` (executor commit + engine re-upload in one arc) |

### W1.9 — adversarial round 3, code analysis (planned 2026-07-18); landed red-first, all rows passing (2026-07-19)

| ID | Case | Status |
|---|---|---|
| R19 | Cross-tree fold collision (local `Readme.txt` vs remote `README.txt`, both new, diff content): the §4.2 conflict fires — conflict copy + one canonical Drive file in the remote's byte-form; no blind upload of a fold-duplicate, no quarantine. A baseline row whose id was fold-skipped from the snapshot is a reported skip, never a remote-delete (was: Drive gained a permanent case-duplicate and the local file was quarantined) | passing (2026-07-19) — reconcile: `TestR19FoldCollisionBothNewConflict`, `...BothNewAdopt` (case-only rename + Record), `...NoFoldTwoDistinctFiles` (no folding → genuinely two files), `...ShadowedBaselineFileHeldHarmless` + `...DirHeldHarmless`; engine: `TestR19CrossMachineFoldConflictNoQuarantine` (probe-gated, APFS: both contents survive, no quarantine, no fold-duplicate pair on Drive). **Dir arm deferred, recorded** (decisions.md "W1.9.1") |
| R20 | Remote deletion of a folder whose local copy holds only ignored/temp leftovers (`.DS_Store`) quarantines cleanly — leftovers travel to quarantine with the dir; an unexpected survivor still refuses (was: `directory not empty` every cycle, forever) | passing (2026-07-19) — `TestR20QuarantinedDirSweepsIgnoredLeftovers` (executor: ignored + temp both carried, dir removed), `TestR20QuarantinedDirStillRefusesUnexpectedSurvivor` (executor: real file refuses, untouched), `TestR20RemoteDeletedDirWithDSStoreUnwedges` (engine: tracked child rescued, second cycle idle) |
| R21 | Two same-day quarantines of one rel_path keep both rescue copies (destination uniquifies; `moveFile` refuses an existing destination) (was: the second silently overwrote the first) | passing (2026-07-19) — `TestR21QuarantineNeverOverwritesRescueCopy` (executor: v1 and v2 both survive), `TestR21GuardedMoveFileRefusesOccupiedDestination` (belt-and-braces beneath the uniquifier), `TestR21SameDayRequarantineKeepsBothCopies` (engine: delete → restore → delete, both copies in quarantine) |
| R22 | Type clash at one rel_path (remote dir vs local file, and the mirror): reported skip, no transfer, no delete, no standing failure loop, no same-name pair minted on Drive (was: `mkdir_local` error loop + a file uploaded beside the folder) | passing (2026-07-19) — `TestR22NewLocalFileVsRemoteFolderSkips` + `...NewLocalDirVsRemoteFileSkips` (reconcile: empty plan, skip reported), `TestR22RemoteFolderVsLocalFileNoMintNoLoop` (engine: Drive holds only the folder, local file untouched, second cycle steady) |
| R23 | The OAuth auth URL carries an S256 PKCE challenge and the token exchange carries its verifier | passing (2026-07-19) — `TestR23LoginUsesPKCE` (`internal/auth`: full loopback flow against a fake token endpoint — the test plays the browser; asserts `code_challenge`/`S256` in the URL, `code_verifier` in the exchange, S256(verifier) == challenge, token persisted) |

### W1.9.6 — adversarial review round 4 (2026-07-21); landed red-first, passing

| ID | Case | Status |
|---|---|---|
| R24 | A tracked file **under** a shadowed folder (a duplicate or fold-colliding sibling won "first by id") is held harmless, never quarantined — the "a name collision never sends content to quarantine" guarantee extends to the whole shadowed subtree, not just the directly-colliding row (was: Snapshot skips the folder without walking it, so its descendants read as remote-deleted and were quarantined off disk) | passing (2026-07-21) — `TestR24DescendantOfShadowedFolderNotQuarantined` (engine, probe-gated/APFS: fold-equal sibling wins first-by-id, the child stays put, quarantine empty, both rows surfaced as skips), `TestExpandShadowedCoversSubtree` (engine unit: a shadowed folder id drags its tracked subtree into the harmless set) |

### W4 — randomized convergence fuzzer (2026-07-23); found R25, landed red-first

| ID | Case | Status |
|---|---|---|
| FZ1 | Seeded random-ops fuzzer, N machines + interleaved syncs + one-shot crashes at executor checkpoints. Oracle (all exact/non-flaky): **§4.5 structural invariant** on every plan; **eventual convergence + idempotence** (all machines reach a zero-action, zero-failure fixed point within a bound); **no silent divergence** (at the fixed point every machine's file/dir tree is byte-identical to every other's and to the reconstructed Drive tree); **identity stability, scoped** (a clean single-writer rename of an unmodified file is a MoveRemote preserving the Drive id with zero delete-class actions — the quiet-rename probe). Deterministic replay from seed (pinned clock + monotonic mtimes). Op menu covers the shipped-bug classes whose correct outcome is convergence (S4/C4/S7/A1/R7/R6/A4/C2-files); by-design non-convergent classes are covered by their own rows, not the oracle (decisions.md "W4") | passing (2026-07-23) — `TestFuzzConvergence` (`internal/engine/fuzz_test.go`); default bounded (8 seeds × 70 steps), `SYNCKEEPER_FUZZ_*` env widens it, `-short` shrinks it. Green + `-race` |
| R25 | A baseline file whose remote moved to a new path, with the local file already at that new path (the same rename made locally, or a crashed MoveLocal left disk+Drive ahead of the baseline): recorded in place, no upload/download collision (was: pass 1 planned a Download to "restore" the remote file while pass 2 uploaded the "new" local file at the same path → §4.5 refused the whole plan every cycle → permanent wedge) | passing (2026-07-23) — reconcile: `TestR25CoincidentMoveRecordsInPlace` (single Record, id preserved), `TestR25CoincidentMoveDivergentContentConflicts` (diverged content → conflict copy + remote wins); found by FZ1, minimized red-first |

### W3 — watcher modularization + FSEvents (2026-07-23)

| ID | Case | Status |
|---|---|---|
| W3.2-fsevents | The macOS FSEvents backend (`//go:build darwin && cgo`) wakes the sync loop on a real local change and filters ignored paths (`.DS_Store` etc.) before waking | passing (2026-07-23, macOS) — `TestFSEventsBackendWakesOnChange` (integration: a real write wakes within the latency window; `refresh` returns 0 failed), `TestFSEventsShouldWakeFiltersIgnored` (deterministic filter unit test). Existing watch suite (R14 reload race, R15 rebuild/creation failure) pinned to the fsnotify backend via `TestMain` and green under `-race`; pure-Go (`CGO_ENABLED=0`) build + watch tests green (fsnotify fallback) |
| W3.4-rebuild | The periodic watcher rebuild is per-backend (`needsRebuild()`): a no-rebuild backend (FSEvents) is left running, a rebuild backend (fsnotify) is recreated at the cadence | passing (2026-07-23) — `TestRebuildIsPerBackend` (fake backend via the `newBackend` seam: `needsRebuild=false` → created exactly once, never recreated at cadence; `true` → recreated), R15 still drives the real fsnotify rebuild→degrade→recover path, `TestFSEventsBackendWakesOnChange` asserts the real FSEvents backend reports `needsRebuild=false` |
| W3-adv-1 | The test-harness watcher stop waits for the daemon to fully exit before settle-phase syncs run (soak-gate integrity: an in-flight daemon cycle racing a direct `engine.Sync` violates the engine's cycle serialization and can double-plan an upload, minting a duplicate-name pair on the fake Drive → a spurious red after 2 h) | passing (2026-07-23) — `TestStopWaitsForDaemonExit` (red-first: pre-fix the stop returned with the daemon still `running=true mode="watching"`); `startWatcher`'s stop is now `cancel(); <-done`, `rebuild_test` gains the same cleanup |
| W3-adv-2 | FSEvents never wakes on churn under an ignored directory — the ignore filter is component-wise relative to the stream root, restoring parity with fsnotify (which never watches inside ignored dirs) and the scanner (which skips those subtrees) | passing (2026-07-23, macOS) — `TestFSEventsShouldWakeFiltersIgnored` extended (file under ignored dir; ignored dir itself; clean sibling still wakes; the root itself wakes; out-of-root falls back to basename); red-first: the basename-only filter woke on `node_modules/pkg/index.js` |

| W1-scale | ≥50k files under the daemon: no fd exhaustion; watcher kill → polling → recovery | passing (2026-07-23, macOS) — `TestScale` + `TestFSEventsScaleNoFDExhaustion` (gated by `SYNCKEEPER_SCALE_FILES`, acceptance = 50000). At 50k: FSEvents 0/500 dirs unwatchable (no per-file fds); fsnotify 403/500 unwatchable at the 10240 fd limit → failure latch trips → polling (graceful; the kill→poll→recover loop is R15); a full sync of 50 500 actions converges, second cycle idle |

## Live smoke (any phase, manual)

| Case | Status |
|---|---|
| `SYNCKEEPER_LIVE_TEST=1` round-trip against real Drive (upload/download/trash/changes) | passing (2026-07-08, macOS) |
