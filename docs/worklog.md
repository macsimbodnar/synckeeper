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
- **Time annotation (requested):** started at **18:47 CEST**, a 2 h soak (7200 s chaos + ~2 s pause + up to 60 settle syncs + `doctor` on both machines) completes around **20:52 CEST** — check for the result in the **20:52–21:00 CEST** window. Pass = `--- PASS: TestSoak` with a "converged on N files" line and no `doctor` errors.
