# Status — session-to-session state pointer

The single answer to "where are we?". Read this first; **rewrite it before ending any work session** (sections replaced in place — history lives in `git log` and [decisions.md](decisions.md), not here).

**Updated:** 2026-07-18 — adversarial-analysis session. **W1 is reopened as W1.8; it blocks everything else.** No code changed this session: findings, decisions, and the plan are recorded, implementation is the next session's work.

## Last completed

**A second adversarial analysis of the whole engine against the goal/plan/spec (2026-07-18).** Nine defects found, each reproduced with a throwaway test before being reported; the probes were deleted and the tree left clean (build/vet/test/gofmt green, working tree untouched). Three matter structurally:

- **A1 — directory identity is unprotected.** A plain local folder rename plans `mkdir_remote` + per-file `move_remote` + `trash_remote`; the Drive folder id changes. Local-driven pairing excludes directories (`reconcile/plan.go:256-265`); the remote→local direction was already handled, so only this direction was missing — and spec §4.3 never said which way its rule ran.
- **A2 — that wedges the mass-delete guard.** Renaming one folder with 20 subfolders = 21 deletes / 41 tracked items: the one-shot aborts demanding `--confirm-deletes`, the daemon defers the deletes without changing the state that produced them and stays **permanently guard-blocked**. The guard meant to catch catastrophes was firing on the most common file operation.
- **The choke-point observation.** The same stat-guard had been added four times (R4/R6/R7/R8) at one more call site each, and `quarantineLocal` was still unguarded (A7).

The rest: A3 `reload`↔event-pump data race (confirmed under `-race`), A4 decision-table row skipped under a moved ancestor (also emits two actions for one rel_path), A5 spec §4.5's concurrency rule normative but unimplemented, A6 daemon exits on watcher-rebuild failure, A8/A9 low-severity hardening, plus testing.md N-row drift.

Decisions taken (Max, on agent analysis — see [decisions.md](decisions.md) 2026-07-18):

1. **The local-write gate**, with mechanical enforcement — a single file may mutate a local path, and an AST test fails any raw FS call outside it. The `internal/localtree` capability-package version was judged right-idea-wrong-time; revisit if a second component ever writes into the sync dir.
2. **The mass-delete guard counts content, not containers** — file-class deletions against tracked non-directory items.
3. **W4 moves ahead of W3.** Correctness never depends on the watcher (spec §8.1), and three consecutive adversarial passes have found engine bugs the suite missed. W4's oracle is strengthened first: identity stability + the §4.5 structural invariant — the originally specced oracle would have passed A1 clean.
4. **A1's directory pairing: share the representation, not the detection.** Local-driven dir moves are collapsed out of the file pairing that already happens, under a deliberately conservative rule (a missed pairing costs a folder id; a wrong one reparents a whole remote subtree). This also corrected the spec text written earlier the same day — a renamed directory cannot pair "by file id", because the new local directory has never been synced and has no id; the only evidence is its children. Two verified implementation traps are recorded with it (the moves-stage `IsDir` split, and `moveRemote` dropping `IsDir` on commit).

Docs updated this session: spec §4.2/§4.3/§4.5/§6/§7/§8.3/§16 + Roadmap; plan.md (W1.8 added, W3↔W4 execution order swapped, risk table); testing.md (R9–R17 + gate test, N1/N3 given rows and IDs, drift removed); decisions.md (two entries).

Earlier this run — **W2 closed** (2026-07-18): W2.1 dead knob removed, W2.2 normalization folding, W2.3 stale reload hint, W2.4 BYO credentials, W2.5 README/Makefile build policy.

## In progress

Nothing in flight. Docs committed on master (**not pushed** — Max pushes). No code changes pending.

## Next

**W1.8 — adversarial correctness fixes, round 2** ([plan.md](plan.md) W1.8): nine items, **in the listed order**. Item 1 is the local-write gate, because everything after it is written against that gate; items 2–3 (directory renames as moves, guard counts files) are the highest user-visible impact. Discipline is W1.7's: **red-first regression test, then the fix, then the testing.md row**, suite + `-race` green at every commit.

Every design question is closed — plan.md carries the decided approach inline, decisions.md carries the reasoning and the rejected alternatives. **Implement, don't re-derive.** If something in the plan turns out to be wrong when it meets the code, that is a decisions.md entry, not a silent deviation (W1.6 is the precedent: the analysis was wrong, the test proved it, and the correction was recorded).

Then **W4** (fuzzer, with the strengthened oracle), then **W3** (fswatch + FSEvents + 50k scale + soak re-run).

## Blockers / parked

- **W6** (real multi-machine rollout): needs a second physical machine — Max's step.
- **Optional, Max:** rotate the OAuth client in the Google console before publication (hygiene; the decided credential model doesn't require it).
- **Deferred to publication:** the README donation note (W2.4d) — repo is still private.
- **Carried into W3 to size, not a defect:** every cycle that writes files fires its own fsnotify events, re-triggering the loop. Converges and is harmless at personal scale; measure under the W1-scale test before deciding whether a backend should suppress self-inflicted events.
