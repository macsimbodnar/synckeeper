# Synckeeper — Implementation Plan

Master tracking document. The spec in [spec.md](spec.md) is the contract; this file tracks how and in what order the delta between spec and code gets built. Rewritten 2026-07-17 against the revised spec: the phase system (0–7) is retired as history; new work is organized in workstreams.

## Phase history (done, kept for reference)

| Phase | Doc | Outcome |
|---|---|---|
| 0 | [phase-0.md](history/phase-0.md) | Skeleton: auth, config, DB, Drive client interface — done 2026-07-07 |
| 1 | [phase-1.md](history/phase-1.md) | One-shot bidirectional sync, S1–S8 — done 2026-07-08 |
| 2 | [phase-2.md](history/phase-2.md) | Safety hardening, F1–F5 — done 2026-07-08 |
| 3 | [phase-3.md](history/phase-3.md) | Continuous mode + 2 h soak — done 2026-07-09 |
| 4 | [phase-4.md](history/phase-4.md) | `init --adopt` + matrix tests — done 2026-07-14 (real rollout pending, now W6) |
| 5 | [phase-5.md](history/phase-5.md) | Cross-platform hardening — superseded by W7/W8 |
| 6 | [phase-6.md](history/phase-6.md) | Daemon monitoring + control socket — stages 1–2 done 2026-07-14; tray → W9 |
| 7 | [phase-7.md](history/phase-7.md) | macOS quick wins — activity direction + case-collision done; Finder sidebar abandoned (no API); rest folded into W3/W9 |

## Workstreams

Statuses: `not started` → `in progress` → `blocked (reason)` → `done (date)`. Work strictly in order within a workstream; W1 blocks everything else (correctness first).

**Execution order on the primary platform (updated 2026-07-18): W1.8 → W4 → W3 → W5.** W3 and W4 keep their identifiers and swap execution order — correctness never depends on the watcher (spec §8.1), so the fuzzer earns more than FSEvents does right now; see decisions.md 2026-07-18 "Roadmap". W6+ follow the spec roadmap.

### W1 — Correctness fixes (from the 2026-07-17 review) — `done (2026-07-17)` · reopened as W1.7, then W1.8, by adversarial analysis

Reproduced bugs first; each fix lands with its regression test (testing.md R1/R2, plus rows below). **A later adversarial re-analysis found a critical data-loss bug this ordering-focused pass missed — see W1.7 below; that too must be green before W2.**

1. **[R1] Conflict-backup vs move ordering — data loss.** — **done 2026-07-17.** Remote rename to a lexicographically smaller path + remote edit + local edit: `ConflictBackup` sorted before the `MoveLocal` that fed it, backup failed, download overwrote the moved local edit; cycle 2 planned nothing. Fixed per spec §4.5: a true conflict never routes the local content through a move — the backup acts on the file's current location (conflict copy named after it) — and actions carry `ProtectedBy`, refused by the executor when their backup failed. Tests: see testing.md R1 (engine + reconcile + executor levels).
2. **[R2] MkdirLocal-before-MoveLocal — permanent livelock.** — **done 2026-07-17.** Remote same-id dir rename + new remote subdir inside it: the mkdir stage `MkdirAll`ed the move destination; `MoveLocal` then failed `file exists` every cycle. Fixed with the ordering fix per spec §4.5: plan stage order is now dir-moves → mkdirs → file-moves/backups → transfers → deletes. Dir moves depend on nothing the plan creates; file moves stay after mkdirs because a `MoveRemote` may target a folder a `MkdirRemote` creates. Tests: see testing.md R2.
3. **`init --force` leaves a stale remote mirror** — **done 2026-07-17.** It reset the page token without rebuilding the cache → silently missed remote changes. Fixed per spec §12: `initialize` now calls `remotedelta.ForceFullWalk` (fresh token + mirror rebuild in one step); a fresh `init` gets a pre-warmed mirror as a side benefit. Test: testing.md R3.
4. **Download overwrite window** — **done 2026-07-17.** Downloads now carry the local state the plan assumed at the target (scanned size/mtime, or "absent" for conflict-vacated and edit-beats-delete targets); the executor re-stats immediately before the atomic rename and refuses on any drift — the racing local write wins the cycle and the next cycle resolves it as an ordinary conflict. Tests: testing.md R4.
5. **Read-only commands can migrate the DB without the lock** — **done 2026-07-17.** `statedb.OpenRead` accepts only an exact schema-version match (older → "run `sync` once with this binary"; newer → "binary too old"; missing DB never created); `openReadEnv` uses it, treating a missing DB as not-initialized. Migrations now run only under the instance lock (spec §14). Test: testing.md R5.
6. **Swap-rename sweep** — **done 2026-07-17, and the review's "self-heals" analysis was wrong.** The verification test exposed *silent permanent divergence*: after the swap's moves half-failed (FS renamed, DB rolled back), an unprotected `Record` stamped the planned md5 onto whichever file sat at the path — a poisoned baseline the scanner trusted forever. Fixed by extending `ProtectedBy` to local moves (records/uploads of a moved file are refused when its move failed) and by `Record` verifying the scanned stat before overwriting the baseline (invariant 7). The transient unique-constraint move failures remain as accepted noise; convergence to the *correct* state within ~4 cycles is now pinned by the test. Tests: testing.md R6.

### W1.7 — Adversarial-analysis correctness fixes — `done (2026-07-17)`

Reopened W1 after an adversarial re-analysis found a confirmed critical data-loss bug the ordering-focused W1 review missed. Same discipline (red-first regression tests, suite + `-race` green). Blocks W2. See decisions.md 2026-07-17 "W1.7 (R7/R8/G3)".

1. **[R7] `MoveLocal` clobbers an untracked local file at the move destination — silent data loss.** — **done 2026-07-17.** A remote rename/move onto a name that already exists locally `os.Rename`d over the local file (no conflict copy, no quarantine, reported as success). Fixed with the R4 pattern generalized to moves: reconcile preserves an untracked occupant as a conflict copy (backed up before the move, uploaded, move `ProtectedBy` the backup) and stat-pins a tracked occupant (safe to clobber, e.g. the R6 swap); the executor re-stats the destination and refuses an unexpected occupant. Tests: testing.md R7 (reconcile + engine + executor).
2. **[R8] `ConflictBackup` overwrites an existing destination.** — **done 2026-07-17.** Same unguarded-rename class; the executor now refuses a rename onto an existing conflict-copy path (crash leftover), replanning with a fresh timestamp. Tests: testing.md R8.
3. **[G3] Mass-delete guard aborted the whole cycle (spec §6/§8.1 deviation).** — **done 2026-07-17, C1.** The daemon now defers only the delete-class actions and keeps syncing everything else, surfacing the block in `status`; the interactive one-shot still aborts with the `--confirm-deletes` hint. New `engine.Options.DeferMassDelete` + `Result.GuardBlocked/GuardReason`. Tests: testing.md G3.

### W1.8 — Adversarial-analysis correctness fixes, round 2 — `not started`

Reopens W1 again after a second adversarial analysis (2026-07-18, run against the goal/plan/spec once W2 closed) found nine defects, each reproduced with a throwaway test before being reported. Same discipline as W1.7: **red-first regression test, then the fix, then the testing.md row, suite + `-race` green at every commit.** Blocks everything else. See decisions.md 2026-07-18 "W1.8".

Work strictly in this order — item 1 is the choke point everything after it is written against, and items 2–3 are the highest user-visible impact.

1. **[GATE] The local-write gate — one choke point, mechanically enforced.** New `internal/executor/localwrite.go` becomes the only file in the package permitted to mutate a local path: `guardedRename` / `guardedRemove` / `guardedStat` over a single `expectation{exists, size, mtimeNS}`. Port all five primitives onto it — `download` (`executor.go:507`), `moveLocal` (`:323`), `conflictBackup` (`:380`), `record` (`:559`), and **`quarantineLocal` (`:581`), which had no guard at all (A7)**. Expectations map straight across: backup = zero value (absent), quarantine/record = pinned scanned stat, download/move = what the plan assumed, so `Action.LocalExists/LocalSize/LocalMtimeNS` is already the wire format and no new plan surface is needed. Closing A7 also means reconcile stat-pins `QuarantineLocal`, exactly as R4 stat-pinned downloads. Companion test walks the package AST and fails on any `os.Rename`/`os.Remove`/`os.RemoveAll`/`os.Create`/`os.OpenFile` outside the gate file. Amends spec §7. Tests: R13 + the gate's structural test.
2. **[A1] Local directory renames are moves, not delete + recreate.** `reconcile.Plan` pass 2 `continue`s on `loc.IsDir` before the move-pairing block (`plan.go:256-265`), so directories are excluded from local-driven pairing; pass 4 then trashes the orphaned baseline dir (`plan.go:374-377`). Reproduced with no concurrency: renaming `Docs/` → `Papers/` plans `mkdir_remote` + per-file `move_remote` + `trash_remote`, and the Drive folder id changes. Pair directories through the **baseline by file id** (not by content hash — a directory has none) into one remote rename/reparent. Watch the interaction with the existing `dirMoves` rewrite machinery (`plan.go:52-82`), which already handles the remote→local direction, and with pass 4's resurrect rules (§4.4). Amends spec §4.3. Test: R9.
3. **[A2] The mass-delete guard counts content, not containers.** Falls out of A1 but fix it independently as belt-and-braces: `guards.CheckMassDelete` counts only file-class deletions against tracked non-directory items. Reproduced: renaming one folder with 20 subfolders = 21 delete-class actions / 41 tracked items → the one-shot aborts demanding `--confirm-deletes`, and the daemon defers the deletes without changing the state that produced them, so it replans the same 21 every cycle and stays **permanently guard-blocked**. Verify both halves: one-shot no longer aborts, daemon no longer wedges. Amends spec §6, adds acceptance G4. Test: R10.
4. **[A4] Decision-table rows resolve at post-move paths.** Pass 2 looks up `newRemoteAt(p)` at the *pre-move* path (`plan.go:266`) while emitting the action at `target`, the post-move path (`plan.go:255`, `:295`) — the directory branch three lines above already uses `target` (`plan.go:261`), which is what makes this an oversight rather than a decision. Consequences: spec §4.2's `absent | new | new, diff md5` → conflict never fires (no conflict copy for the local content), and the plan emits `upload B/x` **and** `download B/x` into the same parallel stage. Amends spec §4.2. Test: R11.
5. **[A5] Enforce spec §4.5's concurrency rule instead of assuming it.** No code anywhere implements "same rel_path, or an ancestor/descendant pair, never run concurrently"; `executor.Apply` fans the transfer stage into 4 workers with no overlap check (`executor.go:139-159`). Add a pre-execution assertion so a planner mistake is a refused plan, not a race — this catches A4's class mechanically. Amends spec §4.5. Test: R12.
6. **[A3] `reload` must publish hot config safely.** Confirmed under `-race`: `applyReload` writes `w.Eng.Cfg.Engine.Ignore` (`control.go:123`) while the fsnotify event pump reads it per event (`watch.go:234`, and via `watchSubtree` at `:279`); `w.Poll` (`control.go:127`) is the same shape. The method's comment ("runs on the sync loop, so no cycle is concurrently reading the config") is true for cycles and false for the watcher goroutine. The suite is race-clean today only because no test reloads while events flow — so the regression test must generate event load. Amends spec §8.3. Test: R14.
7. **[A6] The daemon never exits on a watcher-rebuild failure.** `watch.go:171-177` returns the error from `startNotifier`, killing the daemon — against `Run`'s own doc comment, spec §8.1 ("without exiting") and §10 ("falls back to pure polling"). The `pollingOnly` retry branch ten lines above (`:162-167`) handles the identical error by degrading, which is the tell. Trigger is `fsnotify.NewWatcher()` failing under fd pressure — precisely what the surrounding latch machinery exists for. Code fix only; spec is already correct. Test: R15.
8. **[A9 + A8] Two low-severity hardening items.** (a) A crashed directory `MoveLocal` — renamed on disk, DB not yet committed — plans `TrashRemote` for the dir when it is empty (`plan.go:374-377`: `hasCreateUnder` and `remoteAliveUnder` both false). It converges next cycle, but a crash produced a remote delete, the shape invariant 6 exists to rule out; non-empty dirs are already saved by `hasCreateUnder`. (b) `remotedelta.Snapshot`'s BFS has no visited set (`remotedelta.go:240-285`) — unlike `prune`'s directly above (`:168-179`) — so a parent cycle in the cache is an infinite loop in the daemon. Tests: R16, R17.
9. **[DOCS] Reconcile the testing.md N-row drift.** The Names table lists case-collision as `todo` while the daemon table records the same case passing (`TestSnapshotCaseCollision`); spec §16.5 names N1–N3 but only N2 has a row. Give N1 and N3 rows and IDs, and delete the duplicate. No code.

### W2 — Spec alignment & hygiene — `done (2026-07-18)`

1. Remove `full_rescan_interval_secs` (config, validation, reload, docs) — spec §13. — **done 2026-07-17.** Field dropped from `EngineConfig`, its default, validation, and the `reload` hot-copy; the reload test TOML no longer sets it. (Unknown keys are already rejected, so a stale key in a user config surfaces as a named error.)
2. Unicode normalization folding: extend the fold in `remotedelta.Snapshot` + `names.CaseInsensitiveFS`-style probe to NFC/NFD (spec §5); tests N2. — **done 2026-07-18.** Added `names.NormalizationInsensitiveFS` (probe by writing an NFC name and statting its NFD form) and `names.FoldKey` (NFC → lower → NFC, so a combined case+normalization collision collapses correctly); `Snapshot` now takes `caseFold, normFold` and reports the collision's cause (case / normalization / both). `golang.org/x/text/unicode/norm` promoted to a direct dep. Probe validated on real APFS.
3. Stale text: `cmd/synckeeper/config.go` still says live reload is future. (The phase-7 exit-criterion staleness was fixed 2026-07-17 with the docs restructure.) — **done 2026-07-18.** `config` now points at `synckeeper reload` for hot fields and names the restart-only fields; the parked gofmt drift in `cmd/synckeeper/status.go`, `internal/driveclient/driveclient.go`, `internal/service/status_test.go` is cleaned (whitespace-only; `internal/config/config.go` was already reformatted in W2.1).
4. Credentials (decided 2026-07-17, spec §9 — rclone model): (a) keep the author's client id/secret embedded via `internal/auth/credentials.go` as the shipping default; (b) add the BYO override — load `credentials.json` from the config dir (or config keys) with precedence over embedded; (c) `account` reports which credentials are in use; (d) README gains an rclone-style "use your own client id for dedicated quota" page and, at publication time, a donation note (funds future Google verification if the ~100-user unverified cap is ever reached); (e) hygiene: drop the stray `client_secret_*.json` from the repo root — `credentials.go` is the single embedded source. Optional (owner: Max): rotate the client in the console anyway, since publishing makes the historical secret live forever. — **done 2026-07-18.** `resolveClient(configDir)` does `credentials.json` → embedded (config-keys tier dropped — it would leak the secret via `synckeeper config`; see decisions.md); `oauthConfig` threads the config dir into `TokenSource`/`Login`; `account` shows the active client + id; stray file removed, `.gitignore` blocks BYO files; README "Credentials" section added. Donation note stays deferred to publication. Client rotation remains Max's optional call.
5. README: update build instructions for the native-build policy (no cross-compile matrix, cgo allowed). — **done 2026-07-18.** README build section already stated the native policy; corrected the stale Go version (1.22+ → 1.26+ per `go.mod`), sharpened the `build`/`build-all` (supported vs pure-Go-only-legacy) captions, and added matching comments to the Makefile (CGO_ENABLED note + legacy marker).

### W4 — Confidence: randomized sync testing (spec §16.7) — `not started` · **runs before W3** (2026-07-18)

1. **[FZ1]** Seeded fuzzer over the fake backend: N simulated machines, random op streams (create/edit/move/delete/case-rename/conflict), random crash points at executor checkpoints, interleaved syncs. Invariants: eventual convergence, no content loss (every written content survives somewhere legitimate), idempotence of replays. Deterministic replay from seed on failure.
2. **Oracle additions decided 2026-07-18 — not optional.** (a) **Identity stability:** a file or folder id survives a rename, and an op the user expressed as a rename plans zero delete-class actions. (b) **§4.5 structural invariant:** no two same-stage actions on one rel_path or an ancestor/descendant pair (shares the assertion built in W1.8.5). Rationale: W1.8's A1 converges and loses no content, so the originally specced oracle would have passed it clean.
3. **Op menu must include** the classes that have actually shipped bugs, not just the tidy ones: remote move onto an existing local name (W1.7's R7), same-cycle cross-rename swaps (R6), a new local file under a remotely-moved directory (W1.8's A4), directory renames on both sides (W1.8's A1).
4. Wire into CI as a bounded-time run; long runs manual. The 2 h soak stays coupled to W3 (re-run once the watcher backend changes, spec §16.9).

### W3 — Watcher modularization + FSEvents (spec §10) — `not started` · **runs after W4** (2026-07-18)

1. Extract the `fswatch` module interface from `internal/watch`; current fsnotify backend becomes one implementation; poll-only stays the universal fallback; keep the latch/rebuild belt-and-braces.
2. FSEvents implementation for the primary platform (cgo; directory-tree stream, no per-file descriptors). Native build only — first cgo in the repo, Makefile/CI adjusted.
3. **[W1-scale]** acceptance test: ≥50k-file tree under the daemon, no fd exhaustion; watcher kill → polling degradation → recovery.
4. Retire what the fd-exhaustion era no longer needs (rlimit raise stays; per-cycle rebuild cadence re-evaluated per backend).
5. Re-run the 2 h soak on the new backend (spec §16.9).

*Carried in from the 2026-07-18 analysis, to size while designing the backend:* every cycle that writes files fires its own fsnotify events, which reset the debounce and trigger another cycle. It converges (the next cycle finds nothing) and is harmless at personal scale, but at ≥50k files it means a full rescan chasing every sync. Worth measuring under the W1-scale test before deciding whether a backend needs to suppress self-inflicted events.

### W5 — Daemon-first polish (spec §1, §8) — `not started`

1. `init` offers `service install` at the end (daemon-first onboarding).
2. `account` gains the Google email via one `about.get` call (graceful offline).
3. Persist-pause-across-restart: decide (currently in-memory by design; spec documents it — revisit only if it annoys).

### W6 — Real multi-machine rollout — `blocked (needs second machine)`

Spec §16.10: adopt on a second real machine, a day under `watch`, clean `doctor` on both. Then service reboot check.

### W7 — Second platform (Linux, spec roadmap) — `not started`

fswatch/inotify verification, names rules, service (systemd unit exists), native build, full suite + soak on real hardware.

### W8 — Third platform (Windows) — `not started`

Names hardening (reserved names, illegal chars, trailing dots/spaces, long paths), fswatch/RDCW, rename-replace semantics under the journal (F3 on-platform), Task Scheduler, native build, full suite + soak.

### W9 — UI (after CLI is solid, spec roadmap) — `not started`

Tray/menu-bar app as a separate binary on the control socket (mode icon, sync now, pause/resume, open folder/logs); then file-manager badges where the OS has an API. Strictly a client; no sync logic.

## Working process

1. A workstream item is done when its code, tests, and doc updates land together; check items off here with a date **and rewrite [status.md](status.md)** (last/current/next) before ending the session.
2. Every scope change, spec deviation, or non-obvious choice gets a dated entry in [decisions.md](decisions.md) before or alongside the change.
3. [testing.md](testing.md) rows are the acceptance ledger — add the row with the feature, not after.
4. README build/run instructions stay correct at every commit.
5. The spec is kept current: behavior changes edit spec.md in the same change, marked with a dated note.

## Key risks

| Risk | Mitigation |
|---|---|
| FSEvents semantics (coalescing, event replay, volume boundaries) subtler than expected | Hints are wake-ups only (spec §8.1) — correctness never depends on the watcher; poll fallback; W3 scale test |
| First cgo in the repo complicates builds | Native-build policy (spec §10); cgo confined to the fswatch module; pure-Go fallback compiled everywhere |
| Fuzzer flakiness / nondeterminism | Seeded runs, deterministic replay, bounded CI time |
| Ordering fixes regress existing scenarios | R1/R2 land as reconcile-level tests plus full S/F suite; dependency rules are spec-normative now (§4.5) |
| **Manual review keeps finding engine bugs the suite misses** (W1 → W1.7 → W1.8, three rounds) | W4 moved ahead of W3 with a strengthened oracle (identity stability + §4.5 structural invariant); the local-write gate makes one bug class mechanically impossible rather than review-dependent |
| **A1's fix touches the most load-bearing code in the repo** (pass 2 pairing + pass 4 resurrect rules interact) | Red-first reproduction at reconcile *and* engine level; full S1–S8 + R1–R8 suite must stay green; W4's fuzzer re-checks it with directory renames in the op menu |
| Drive API quota under fuzz/soak | Fake backend for both; live smoke stays small and env-gated |
