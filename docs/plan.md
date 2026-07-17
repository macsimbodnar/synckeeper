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

Statuses: `not started` → `in progress` → `blocked (reason)` → `done (date)`. Work strictly in order within a workstream; W1 blocks everything else (correctness first). W2–W5 order is the recommended sequence on the primary platform; W6+ follow the spec roadmap.

### W1 — Correctness fixes (from the 2026-07-17 review) — `done (2026-07-17)` · reopened as W1.7 by adversarial analysis

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

### W2 — Spec alignment & hygiene — `not started`

1. Remove `full_rescan_interval_secs` (config, validation, reload, docs) — spec §13. — **done 2026-07-17.** Field dropped from `EngineConfig`, its default, validation, and the `reload` hot-copy; the reload test TOML no longer sets it. (Unknown keys are already rejected, so a stale key in a user config surfaces as a named error.)
2. Unicode normalization folding: extend the fold in `remotedelta.Snapshot` + `names.CaseInsensitiveFS`-style probe to NFC/NFD (spec §5); tests N2. — **done 2026-07-18.** Added `names.NormalizationInsensitiveFS` (probe by writing an NFC name and statting its NFD form) and `names.FoldKey` (NFC → lower → NFC, so a combined case+normalization collision collapses correctly); `Snapshot` now takes `caseFold, normFold` and reports the collision's cause (case / normalization / both). `golang.org/x/text/unicode/norm` promoted to a direct dep. Probe validated on real APFS.
3. Stale text: `cmd/synckeeper/config.go` still says live reload is future. (The phase-7 exit-criterion staleness was fixed 2026-07-17 with the docs restructure.) — **done 2026-07-18.** `config` now points at `synckeeper reload` for hot fields and names the restart-only fields; the parked gofmt drift in `cmd/synckeeper/status.go`, `internal/driveclient/driveclient.go`, `internal/service/status_test.go` is cleaned (whitespace-only; `internal/config/config.go` was already reformatted in W2.1).
4. Credentials (decided 2026-07-17, spec §9 — rclone model): (a) keep the author's client id/secret embedded via `internal/auth/credentials.go` as the shipping default; (b) add the BYO override — load `credentials.json` from the config dir (or config keys) with precedence over embedded; (c) `account` reports which credentials are in use; (d) README gains an rclone-style "use your own client id for dedicated quota" page and, at publication time, a donation note (funds future Google verification if the ~100-user unverified cap is ever reached); (e) hygiene: drop the stray `client_secret_*.json` from the repo root — `credentials.go` is the single embedded source. Optional (owner: Max): rotate the client in the console anyway, since publishing makes the historical secret live forever.
5. README: update build instructions for the native-build policy (no cross-compile matrix, cgo allowed).

### W3 — Watcher modularization + FSEvents (spec §10) — `not started`

1. Extract the `fswatch` module interface from `internal/watch`; current fsnotify backend becomes one implementation; poll-only stays the universal fallback; keep the latch/rebuild belt-and-braces.
2. FSEvents implementation for the primary platform (cgo; directory-tree stream, no per-file descriptors). Native build only — first cgo in the repo, Makefile/CI adjusted.
3. **[W1-scale]** acceptance test: ≥50k-file tree under the daemon, no fd exhaustion; watcher kill → polling degradation → recovery.
4. Retire what the fd-exhaustion era no longer needs (rlimit raise stays; per-cycle rebuild cadence re-evaluated per backend).

### W4 — Confidence: randomized sync testing (spec §16.7) — `not started`

1. **[FZ1]** Seeded fuzzer over the fake backend: N simulated machines, random op streams (create/edit/move/delete/case-rename/conflict), random crash points at executor checkpoints, interleaved syncs. Invariants: eventual convergence, no content loss (every written content survives somewhere legitimate), idempotence of replays. Deterministic replay from seed on failure.
2. Wire into CI as a bounded-time run; long runs manual. Re-run the 2 h soak after W3 lands.

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
| Drive API quota under fuzz/soak | Fake backend for both; live smoke stays small and env-gated |
