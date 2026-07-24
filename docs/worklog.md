# Work log

Append-only, chronological record of **what agents actually did in this repo, keyed by the prompt that triggered it** — the console-transcript equivalent, kept as the project's permanent memory. The `## Baseline` recap stays pinned at the top as the starting point; every prompt after it appends one entry at the **bottom** of `## Log`. **Never rewrite, reorder, or delete past entries** — this file is the audit trail, not a summary.

## How to keep this file (the rule)

- **One entry per user prompt** (or per autonomous work session), appended at the end of `## Log`.
- Entry header: `### YYYY-MM-DD HH:MM — <one-line gist>`, then the prompt quoted verbatim (lightly trimmed only for length, marked with `…`).
- Under it, **one short sub-bullet per meaningful action or decision** — precise enough to reconstruct what happened, not a narration. Name the files, tests, and commits touched. Read-only/answer-only prompts get an entry too (note "no repo changes").
- Append as you work, and **commit it** — its own `docs: worklog …` commit, or folded into the work's commit.
- This log is granular history; it does **not** replace `status.md` (state pointer), `decisions.md` (the why), or `testing.md` (acceptance). Per-commit truth still lives in `git log`.

---

## Baseline — recap of work since project start (2026-07-06 → 2026-07-22)

*Written 2026-07-23 as the starting point. Summarized, not per-prompt; commit-level detail is in `git log --reverse`, the why is in `docs/decisions.md`.*

**Phase system 0–7 (retired to `docs/history/`).** Seeded project docs (spec, plan, phase checklists, test matrix). Phase 0: skeleton — config, OAuth loopback, SQLite state DB, Drive client, `init`/`status`; live OAuth verified, real credentials embedded. Phase 1: one-shot bidirectional sync. Phase 2: safety hardening. Phase 3: continuous mode (fsnotify) — a 2h soak found fd exhaustion; fixed, plus remote-cache prune and polling latch on watch failure. Phase 6: daemon monitoring via DB heartbeat (no IPC) + control socket (sync/pause/resume/reload). Phase 4: `init --adopt` multi-machine first-merge. Then: replan macOS-first, activity direction, case-collision safety on case-insensitive filesystems, abandoned the Finder-sidebar item (no macOS 14 API), added `login` re-auth.

**Doc/process restructure (mid-July).** Spec rewritten platform-agnostic; work re-organized as workstreams W1–W9; repo restructured as permanent inter-agent memory; retired phases archived. Durability invariant 7 (dependency-aware plan ordering) recorded after an ordering bug shipped.

**W1 correctness fixes (R1–R8/G3).** R1: conflicts never depend on moves; `ProtectedBy` propagation (data-loss fix). R2: plan stage order = dir-moves → mkdirs → file-moves (livelock fix). R3: `init --force` rebuilds the remote mirror, not just token reset. R4: downloads refuse a rename when the target drifted since scan. R5: read-only commands open the state DB without migrating. R6: protect-and-verify records (the swap test disproved "self-heals"). W1.7 (R7/R8/G3): move/backup overwrite guard + daemon defers mass delete.

**W2 cleanup & config.** Removed dead `full_rescan_interval_secs` knob; Unicode NFC/NFD normalization folding (spec §5); refreshed `config` reload hint + cleared gofmt drift; bring-your-own OAuth client via `credentials.json` (embedded default kept); README/Makefile build-policy consistency.

**Adversarial rounds 2–3 + docs.** Round-2/round-3 code-review fix plans; **MANUAL.md created** (end-user manual, new rule: kept true every commit); README/MANUAL consolidation (one owner per audience); roadmap reorder; W4 oracle scoped to the concurrent stage.

**W1.8 — the local-write gate + hardening (9 items).** 1: local-write gate, one mechanically-enforced choke point. 2: local dir renames = one remote move (A1) + commit never restates local truth. 3: mass-delete guard counts content, not containers. 4: decision-table rows resolve at post-move paths. 5: spec §4.5 transfer-stage overlap refused + reconcile property net. 6: reload publishes a hot config snapshot (pump never reads the loop's field). 7: watcher-creation failure degrades to polling (at rebuild and launch). 8: crashed dir-move replays + `Snapshot` terminates.

**W1.9 — fold / type / PKCE (6 items).** 1: fold collisions get their conflict (files resolve cross-tree; shadowed rows held harmless; **directory arm deferred, recorded**). 2: dir quarantine sweeps invisible leftovers instead of wedging. 3: quarantine never overwrites a rescue copy (uniquify above, refuse beneath). 4: type clashes are skips in every direction. 5: S256 PKCE on the OAuth loopback flow, proven end to end. 6: a shadowed folder's tracked subtree is held harmless, never quarantined.

**Round-4 review (doc-only).** MANUAL §8 surfaces the tracked case-only file-rename loop (workaround: edit contents too); code fix stays the deferred dir-arm follow-up.

**State at baseline.** W1 / W1.8 / W1.9 / W2 complete; every reproduced defect from four adversarial rounds fixed. One narrowed Known-bug: fold-equal *folders*, owned by the deferred gate-directory-arm follow-up. **Next: W4** (randomized/fuzzer convergence testing), then W3. Never pushed — Max pushes.

---

## Log

### 2026-07-23 12:30 — scan docs & git log for a recap of past runs
> "scan documenation and git logs and list me in a minimal way (as little as possible to read) of what was done in the last runs of past agents"
- Read `docs/status.md` and `git log`; returned a minimal summary of the last runs (W1.8/W1.9 close-out, next = W4). No repo changes.

### 2026-07-23 12:35 — add a work-log rule + this file
> "add a new rule to the project for the agents … a new file called work logs or similar … track every action done … the prompt, the time and date, and then under it … every action/decision … short but precise … enough for reconstruction … appended … committed … first entry a recap of work done since beginning … on the top as a starting point"
- Created `docs/worklog.md` (this file): append-only, prompt-keyed action log with the baseline recap pinned on top and the keeping-rule stated inline.
- Wired the rule into `CLAUDE.md` — added a doc-map row and a hard rule requiring per-prompt worklog entries.
- Recorded the new doc + agent rule in `docs/decisions.md`.
- Verified `go build ./... && go vet ./... && go test ./...` green, then committed (docs-only).

### 2026-07-23 12:45 — W4: build the randomized convergence fuzzer (next step in the plan)
> "let's move with the next step in the plan"
- Explored the harness: reused the S-series multi-machine `machine`/`newWorld`/`newMachine` helpers, `executor.FaultHook` checkpoints, the in-memory `driveclient.Fake`, `reconcile.ValidateTransferStage`.
- Wrote `internal/engine/fuzz_test.go` (`TestFuzzConvergence`): N machines, random op streams over a small shared namespace, interleaved syncs, one-shot crash injection at executor checkpoints, driven to a quiescent fixed point; oracle = §4.5 on every plan + convergence/idempotence + byte-identical machine/Drive trees + a scoped identity-stability "quiet-rename probe". Bounded default (8 seeds × 70 steps), `SYNCKEEPER_FUZZ_*` env to widen, `-short` to shrink.
- **First run found R25** (seed 3, then seed 39 no-crash): a baseline file whose remote moved to `Q` with the local file already at `Q` (same rename made locally, or a crashed MoveLocal) → reconcile planned `download Q` + `upload Q` → §4.5 refused the whole plan every cycle → permanent wedge. Diagnosed at the reconcile level (pass 1's `!locOK` "move beats delete" vs pass 2's blind upload; `newRemoteAt` returns false because the id is a baseline id).
- Minimized red-first: `internal/reconcile/coincident_move_test.go` (R25, clean + divergent-content cases); confirmed red without the fix (both trip the §4.5 property net), green with it.
- Fixed `internal/reconcile/plan.go` pass 1: when the baseline file is absent at its old path, the remote moved it to a **non-baseline** `Q`, and the local file already sits at `Q`, record it there (same content) or conflict (diverged) — the "non-baseline path" guard prevents mis-firing mid-swap (R6, which regressed on the first attempt and drove the guard).
- Made the fuzzer deterministic: pinned `nowFunc` (frozen during quiesce) removed wall-clock conflict-name churn that caused nondeterministic non-convergence; made the one-shot crash latch `atomic.Bool` after `-race` caught the closure race.
- Verified: full `go build/vet/test` green; `-race` on reconcile/executor/engine green; 60-seed crash sweep + 30-seed no-crash sweep green.
- Docs: testing.md (FZ1 + R25 rows), decisions.md (W4 + R25), spec.md §4.6 + §16.7 notes, plan.md (W4 done, risks), status.md (next = W3). MANUAL unchanged (R25 never a listed bug; W4 is internal).

### 2026-07-23 15:00 — W3.1: extract the fswatch backend interface (next step in the plan)
> "proceed with the next step in the plan. Let's stick to the plan"
- Read `internal/watch/{watch,fswatch?,control,status}.go` + tests; found the loop held a concrete `*fsnotify.Watcher` and did fsnotify-specific work (per-dir `Add`, new-dir registration, event filtering, the pump) inline. Test constraints: keep `newNotifyWatcher` (R15 seam) and `failureLatch` (latch_test).
- Created `internal/watch/fswatch.go`: unexported `fsWatcher` interface (`refresh(root) int` + `close`), `fsnotifyBackend` implementing it (owns its pump goroutine), `newBackend` factory seam, `newNotifyWatcher` moved here. Wake-ups + ignore globs injected (`wake func()`, `ignore func() []string`) so the loop keeps sole ownership of the debounce timer and config (R14).
- Rewired `watch.go`: `startNotifier` is now a thin `newBackend(...)` call; loop `fw` is the interface (`.close()`, `fw.refresh(...)`); `latchIfNeeded` takes `*fsWatcher`; removed `syncWatches`/`watchSubtree` and the fsnotify/`io/fs`/`filepath`/`names` imports; generalized two comments.
- Pure refactor, no behavior change: full `go build/vet/test` green; `internal/watch` tests (incl. R14 reload race + event storm, R15 rebuild/creation failure) green under `-race`; gofmt clean.
- Docs: spec.md §10 (interface-extracted note), decisions.md ("W3.1"), plan.md (W3.1 done, W3 in progress), status.md (next = W3.2 FSEvents). No testing.md row (refactor, no new criterion); MANUAL unchanged (nothing user-visible).

### 2026-07-23 16:00 — W3.2: the FSEvents backend (the repo's first cgo)
> "let's go with w3.2"
- Read the Makefile (`export CGO_ENABLED=0`, comment: "when cgo lands this drops") and confirmed no `TestMain` in the watch tests; matched the `newFSNotifyBackend` signature.
- Wrote `internal/watch/fsevents_darwin.go` (`//go:build darwin && cgo`, ~140 lines of hand-rolled cgo — no wrapper dep): one recursive **file-level** FSEvents stream on a serial dispatch queue; C callback → exported `goFSEventsCallback` via an integer handle registry (Go pointers can't ride in the C context); `refresh` a no-op returning 0; `shouldWake` filters ignored paths (`.DS_Store`) before waking; `close()` = Stop+Invalidate+Release then drop the handle (not idempotent by design). `init()` overrides `newBackend` on darwin+cgo.
- Wrote `internal/watch/main_test.go` (`TestMain` pins `newBackend = newFSNotifyBackend` so R14/R15 and the fsnotify wake tests stay meaningful) and `fsevents_darwin_test.go` (real-change wake integration test + deterministic `shouldWake` unit test; fixed an early double-close hazard).
- Makefile: dropped the global `CGO_ENABLED=0`; `build`/`test`/`vet` native (cgo on → FSEvents), `build-all` pure-Go cross-compile (`CGO_ENABLED=0` per target, fsnotify fallback).
- Verified: cgo compiles/vets clean (dispatch_release, `C.FSEventStreamRef` in a Go struct, the export all fine); FSEvents tests pass (wake in 0.01s); full watch suite green under `-race` with cgo; **fallback proven** — `CGO_ENABLED=0` full build + watch tests green (fsnotify), and darwin/linux/windows pure-Go cross-compile all green; `make build` → native Mach-O arm64, `make build-all` → all platforms.
- Docs: spec.md §10 + build policy (FSEvents implemented), plan.md (W3.2 done), decisions.md ("W3.2"), testing.md (W3.2 row), status.md (next = W3.3), README (`build-all` caption). MANUAL unchanged (backend is invisible to the user; "watches, falls back to polling" still accurate).

### 2026-07-23 18:00 — W3.3: the W1-scale acceptance (≥50k files)
> "let's go with the next step w3.3"
- Read `soak_test.go`, the watch test helpers (`newMachine`/`startWatcher`), and `fdlimit_unix.go`. Key realization: fsnotify's kqueue holds one fd **per watched file**, so 50k exhausts the 10240 soft limit and degrades to polling — while FSEvents holds none. So the acceptance is: FSEvents proves "no fd exhaustion" crisply; fsnotify demonstrates the real fd pressure that trips the latch (the kill→poll→recover *loop* is already R15, scale-independent); the engine must handle a 50k scan.
- Wrote `internal/watch/scale_test.go`: `buildScaleTree` (100 files/dir) + `TestScale`, gated by `SYNCKEEPER_SCALE_FILES` (skip unless set, like the soak). Asserts: full engine sync converges + idempotent second cycle; fsnotify backend creates without error, and when `failed>0` the `failureLatch` trips. Added `TestFSEventsScaleNoFDExhaustion` (darwin+cgo) asserting FSEvents `failed==0` + still wakes at scale.
- Smoke at N=2000 green; **acceptance at N=50000 green** — FSEvents 0/500 unwatchable, fsnotify **403/500** unwatchable (fd pressure, soft limit 10240), engine synced **50 500** actions, second cycle idle (~24 s total). Both cgo modes vet clean; gofmt clean; default `go test` skips the gated tests.
- Sized the carried-over **self-inflicted-events** question: after a cycle writes files, their watch events fire one **bounded, self-terminating** extra cycle (a full rescan — ~19 s at 50k, sub-second at personal scale), never a loop → suppression is an optimization, deferred (decisions.md "W3.3"). Closed the 2026-07-18 parked item.
- Docs: spec §16.8 (verified), plan.md (W3.3 done), decisions.md ("W3.3"), testing.md (W1-scale row passing), status.md (next = W3.4, with the TestMain-pins-fsnotify caveat for the W3.5 soak), CLAUDE.md (scale + deep-fuzz commands). MANUAL unchanged.

### 2026-07-23 18:35 — W3.4: the periodic watcher rebuild is per-backend
> "let's move with w3.4"
- Read the loop's rebuild machinery (`rebuildEvery = 500`, the `cycle%cadence == 0` branch that closes + recreates the watcher to bound a kqueue fd leak) and confirmed R15 relies on the fsnotify rebuild path.
- Added `needsRebuild() bool` to the `fsWatcher` interface; `fsnotifyBackend` returns true (kqueue per-file fd leak), `fseventsBackend` returns false (no per-file fds). Gated the loop's rebuild branch on `fw.needsRebuild()`; when false it falls through to the normal `refresh` (a no-op for FSEvents) and the stream keeps running. Updated the `rebuildEvery` comment; rlimit raise + polling latch untouched.
- Wrote `internal/watch/rebuild_test.go` (`TestRebuildIsPerBackend`): a fake `countingBackend` injected via the `newBackend` seam proves a `needsRebuild=false` backend is created exactly once (never recreated at cadence) and a `true` one is recreated. Added a real-backend assertion in the FSEvents wake test (`needsRebuild=false`). R15 (real fsnotify rebuild→degrade→recover) still green.
- Verified: build+vet both cgo modes; full watch suite green under `-race` (cgo on) and green (cgo off); gofmt clean.
- Docs: spec §10 build policy (rebuild now per-backend), plan.md (W3.4 done), decisions.md ("W3.4"), testing.md (W3.4 row), status.md (next = W3.5, only item left in W3). MANUAL unchanged.

### 2026-07-23 18:40 — W3.5: run the soak against the FSEvents backend
> "yes, let's proceed" (do the W3.5 code now + smoke; hand over the 2 h gate)
- Problem: `TestMain` pins the watch suite to fsnotify (R14/R15 seams), so `TestSoak` was exercising fsnotify; and the cross-platform `soak_test.go` can't name `newFSEventsBackend` (darwin+cgo-only symbol).
- Fix: `main_test.go` captures `productionBackend = newBackend` (the platform default — FSEvents on darwin+cgo, set by the darwin `init`) *before* pinning fsnotify; `TestSoak` sets `newBackend = productionBackend` for its run and restores the pin. No build-constraint symbol leaks into the cross-platform soak; the soak validates what production runs.
- Smoked: 30 s soak → converged on 89 files, clean `doctor`; 90 s soak → converged on 220 files, clean `doctor` (both against FSEvents; the transient lstat ERRORs are chaos racing the scanner — retried, converged, as designed).
- Docs: plan.md (W3.5 wired+smoked, 2 h gate = Max's), decisions.md ("W3.5"), spec §16.9, testing.md (soak row), status.md (W3 engineering done; next = W5). MANUAL unchanged.
- **Handoff: the full 2 h acceptance gate is Max's release-ritual run:** `SYNCKEEPER_SOAK_SECONDS=7200 go test ./internal/watch/ -run TestSoak -timeout 3h` (runs against FSEvents on macOS). This closes the W3 engineering.

### 2026-07-23 18:55 — defer the 2 h soak to be the last W3 step (after an adversarial check)
> "I changed my mind. I don't want to run this test now. Remove it and stop it. … I will run first an adversarial check and I want to run this long test after that. Annotate as next and last step in W3 the long test — what to run, how to run, what we expect, the goal."
- Confirmed nothing was running (the 2 h soak was never started; earlier background smokes all completed) — nothing to stop. Removed the "run now / check at 20:52" time annotation above.
- plan.md W3.5: re-annotated the 2 h soak as the **explicit final W3 step**, to run **after Max's adversarial check**, with a full run block — command, how-to, expected pass criteria, and the goal.
- status.md: Next is now the **adversarial check first, then the final 2 h soak** (W3 is not closed until it passes); W5 follows W3's close. No code change.

### 2026-07-23 19:09 — Adversarial analysis of W3 (the pre-soak check)
> "perform and adversarial analysis of the project with particular focus on work done in w3. Perform a general check if the code is aligned with the goals and the documented plan over all the project and then docus on the w3. Check if the implementation ir correct, is in line with goals and requirements, if the test coverage is complete. hunt for bugs, errors in logic or any other issue. Generate a report at the end with all the details and present a fix plan and the tests for it that will be executed before the end test for closing the w3."
- General alignment pass: status/plan/spec/decisions/testing/MANUAL/README/Makefile/CLAUDE.md cross-checked against the code — consistent; every W3.1–W3.5 claim verified to exist in the code as described. Verified green now: `go build ./... && go vet ./... && go test ./...`, `go test -race -count=1 ./internal/watch/`, `CGO_ENABLED=0 go test -count=1 ./internal/watch/`. No stray fsnotify references outside `internal/watch`.
- Deep W3 review of `fswatch.go`, `fsevents_darwin.go` (+cgo lifecycle: registry, Invalidate ordering, callback races — sound), `watch.go` loop integration, and all W3 tests. W3.1 confirmed a faithful pump transposition (diff-level).
- **Finding 1 (harness, gates the 2 h soak): `TestSoak`'s settle phase races in-flight daemon cycles** — `startWatcher`'s cancel doesn't wait for `Run` to return, so settle `Sync`s overlap the daemon's last cycle on the same engine (engine is documented cycle-serialized). Can mint a duplicate-name pair on the fake Drive → spurious red on the 2 h gate. Fix: cancel waits on done.
- **Finding 2 (backend divergence): FSEvents wakes on churn inside ignored directories** — `shouldWake` checks only the basename; fsnotify structurally never delivers events under ignored dirs (refresh SkipDirs them). A user-ignored `node_modules`-style dir busy-rescans the daemon under FSEvents only. Fix: component-wise ignore filtering relative to root.
- Finding 3 (low, recorded-not-fixed candidate): a mid-life dead FSEvents stream (external-volume remount) is never detected or rebuilt (`needsRebuild=false` forever); poll covers correctness, `status` overstates "watching". Max's call: accept via decisions.md or add belt-and-braces.
- Finding 4 (test hygiene): `rebuild_test.go` failure path leaks the `Run` goroutine (cancel/wait not in `t.Cleanup`). Finding 5 (nits): CLAUDE.md soak command lacks `-v` (pass criteria read a log line); `status.go` ModeWatching comment still says "fsnotify active".
- Report + sequenced fix plan (fixes red-first before the 2 h gate, per the standing discipline) delivered in-chat; awaiting Max's go-ahead on the fixes.

### 2026-07-23 19:24 — adversarial-check fixes 1+2, red-first, before the 2 h gate
> "next"
- **Fix 1 (F1+F4, harness serialization):** red test `TestStopWaitsForDaemonExit` written first and confirmed red (`stop() returned while the daemon reports running=true mode="watching"`). Fix: `startWatcher`'s stop is now `cancel(); <-done` (waits for `Run` to exit — `rec.stop()` runs before Run returns, so "stopped recorded when stop returns" is the observable proof); `rebuild_test.go` gains the same `t.Cleanup` against the failure-path daemon leak. The soak's settle phase can no longer race an in-flight daemon cycle on the same engine.
- **Fix 2 (F2, FSEvents ignored-dir filter):** red case `file under ignored dir` (`/sync/node_modules/pkg/index.js`, glob `node_modules`) confirmed red against the basename-only `shouldWake`. Fix: component-wise `ignoredPath` relative to the stream root (root always wakes; out-of-root falls back to basename); root `EvalSymlinks`-resolved once and the stream registered on it. Test extended with root/out-of-root/sibling cases.
- Nits folded in: CLAUDE.md soak command gains `-v`; `status.go` ModeWatching comment de-fsnotified.
- Verified: gofmt clean, `go vet` clean, `go test -race -count=1 ./internal/watch/` green, `CGO_ENABLED=0` watch tests green, full `go build && go vet && go test ./...` green.
- Docs: decisions.md two entries ("W3 adversarial check, fix 1/fix 2"), testing.md rows W3-adv-1 + W3-adv-2, spec §10 fswatch cell dated. F3 (dead-stream-never-rebuilt) left as Max's open call, noted in the fix-1 entry.

### 2026-07-23 23:04 — running the 2 h FSEvents soak gate (the W3 close-out test)
> "start the 2h test. Run it by yourself without me babysitting you. Start whatever you need and verify the result at the end of the test. document the result and the process"
- Ran `SYNCKEEPER_SOAK_SECONDS=7200 go test -count=1 ./internal/watch/ -run TestSoak -timeout 3h -v` in the background against commit `aefe4fe` (clean), cgo on → FSEvents. Conditions logged (go1.26.4, kern.maxfilesperproc 10240).
- **Run 1 (19:44 start): killed ~20:20:54, ~37 min in — NOT a test failure.** Log clean throughout (`failed=0`, only the expected transient `lstat … no such file` from chaos racing the scanner). Root cause via `pmset -g log`: system **idle-sleep**. Other apps' sleep-prevention assertions (coreaudiod, powerd "display on", a stray caffeinate) released at 20:20:48; with nothing holding one for `go test`, the machine slept 6 s later and the task died. Machine's `sleep` setting is an aggressive **1 minute**.
- **Run 2 (caffeinate -dimsu, 22:10 start): killed ~22:57, ~47 min in — again system sleep, not a failure.** The ~1.5 h gap between dispatch and the log's real start (22:10:07, at a scheduled `UserWake`) proved the machine had been asleep, suspending my own session too. It died on **battery**, where `caffeinate -s` is void (macOS honors it only on AC) — so the 1-min idle-sleep still won. Log clean to the kill.
- **Run 3 (23:04 start): under `caffeinate -dimsu` on AC power (100%, charged).** On AC, caffeinate genuinely prevents system sleep — confirmed `PreventUserIdleSystemSleep 1` held by the run's caffeinate (pid 95816). FSEvents backend confirmed in-log. This is the proven-effective condition; expected finish ~01:04 + settle. **Result pending — to be appended when the background task reports.**
- **Environmental caveat (documented, not fixable in software from here):** this machine idle-sleeps after 1 min and is intermittently on battery; an unattended 2 h run only survives while on AC with caffeinate holding. If run 3 is unplugged to battery mid-run it will sleep and die again — outside my control. The soak test itself was healthy in every attempt; the gate just needs an uninterrupted 2 h awake window.

### 2026-07-24 09:3x — 2 h soak: runs 3 killed too; switched to a detached (sleep-surviving) run
- **Run 3 (caffeinate on AC, 23:04 start): killed ~23:57, ~53 min in.** Same pattern (37/47/53 min — all end-of-day). Root constraint identified: a Claude Code *harness* background task cannot survive the Mac sleeping — on sleep the harness suspends and the tracked `go test` is torn down (log freezes at the sleep instant, process gone on wake), and the machine's idle-sleep is a 1-minute setting, intermittently on battery where `caffeinate -s` is void. Not a test failure in any run (`failed=0`, clean, convergence-bound throughout).
- **Run 4 (2026-07-24 morning): launched DETACHED** — `nohup caffeinate -dimsu env SYNCKEEPER_SOAK_SECONDS=7200 go test … -timeout 10h -v &`, reparented to launchd (verified PPID=1). A plain OS process is *suspended and resumed* across machine sleep instead of being torn down; Go's monotonic timers pause during sleep, so it accumulates a full 2 h of awake-time chaos and completes across sleep/wake cycles. FSEvents backend confirmed in-log. `-timeout 10h` gives margin since wall-clock may far exceed the 2 h of awake time.
- Tradeoff: a detached run yields no harness completion signal, so a harness "watcher" task (`b73br72tv`) polls the log and notifies on the terminal marker (PASS/FAIL/EXIT); if it dies on a sleep the detached run continues and the watcher is relaunched when next active. **Result still pending — appended when run 4 reports.**

### 2026-07-24 12:05 — 2 h FSEvents soak PASSED → W3 CLOSED
- **Run 4 (detached) PASSED the full 2 h.** `--- PASS: TestSoak (7216.68s)`, **converged on 16,344 files** (both machines byte-identical), clean `doctor` on both, 0 divergence, 0 panic/race. 3,488 transient `lstat … no such file` retries = chaos racing the scanner, converged as designed. Ran continuously 09:39→11:39 CEST (no sleep gaps this time — stayed on AC). Process exited cleanly; no strays left.
- Verified every pass criterion against the full log directly (not just the watcher tail): PASS marker, converged line, FSEvents-backend line, zero FAIL/diverged/never-settled/doctor-after-soak/panic.
- **The detached approach is what made it possible.** PID reparented to launchd (PPID 1) survived the watcher task's own death and any brief sleeps; three earlier harness-task runs (37/47/53 min) were all killed by the 1-min idle-sleep, none a test failure.
- Docs updated (all committed, not pushed): testing.md soak row → passing (2026-07-24, FSEvents); spec §16.9 gate PASSED; plan.md W3 item 5 done + W3 header `done (2026-07-24)` + execution-order line; decisions.md 2026-07-24 "W3 closed" (result + detached-run rationale + future-port guidance); status.md rewritten (W3 closed, next W5, W6/port soak lesson). MANUAL unchanged (backend + soak invisible to users).
- **W3 is closed.** Next workstream: W5 (daemon-first polish). One low FSEvents finding (dead-stream-never-rebuilt) left as Max's open call, recorded.
