# Status — session-to-session state pointer

The single answer to "where are we?". Read this first; **rewrite it before ending any work session** (sections replaced in place — history lives in `git log` and [decisions.md](decisions.md), not here).

**Updated:** 2026-07-19 — **W1.8 items 1–6 are done** (gate; A1 + C1/R18; A2 guard; A4 post-move rows; A5 §4.5 enforcement; A3 reload race). W1.8 still blocks everything else; W1.9 runs right after it. **Reminder for every W1.8/W1.9 fix: the fix's commit also removes its Known-bugs entry from MANUAL.md.**

## Last completed

**W1.8.6 — `reload` publishes hot config safely (2026-07-19).** The sync loop publishes an atomic ignore-glob snapshot (`publishIgnore`) that the fsnotify pump and `watchSubtree` load per use; `applyReload` republishes on swap. Red-first: R14 generates a 2 ms file-write storm during 25 reloads — the pre-fix code raced exactly as the plan predicted (applyReload write vs pump read). One narrowing recorded (decisions.md "W1.8.6"): `w.Poll` has no off-loop reader, so it stays a plain loop-owned write; the race was the globs alone. MANUAL.md retired the reload Known-bugs entry in the same commit. Row R14 passing.

Just before it — **W1.8.5 — spec §4.5 enforced, not assumed (2026-07-19).** `reconcile.ValidateTransferStage` refuses any plan whose concurrent transfer stage overlaps on a rel_path or a file-level ancestor/descendant pair; `executor.Apply` runs it before anything journals, and the reconcile test harness validates **every** planned scenario in the suite as a property net (the same assertion W4's oracle will share) — since the follow-up commit, including the three tests that call `Plan()` directly. One precise exemption, recorded (decisions.md "W1.8.5"): dir Records are DB-only and exempt from the ancestor rule. An adversarial review of the commit confirmed red-first held, the exemption is precise, and the daemon treats a refused plan as log-and-backoff; it also flagged the two doc nits the follow-up commit closed, plus one recorded fold-blindness note for W1.9.1. Row R12 passing.

Just before it — **W1.8.4 — decision-table rows resolve at post-move paths (2026-07-18).** Pass 2's new-item lookup moved from the pre-move path to `target` in both branches (files and dirs): a new local file under a remotely-moved directory now meets its remote counterpart at the post-move path — the §4.2 both-new conflict fires (backup at `target`, where the hoisted dir MoveLocal already put the content), adopt records at `target`, and one rel_path gets exactly one action. Red-first at reconcile (conflict + adopt cases), engine test pins both versions surviving and exactly one file on Drive. Row R11 passing.

Just before it — **W1.8.3 — the mass-delete guard counts content, not containers (2026-07-18).** Directory deletions are excluded from both sides of the fraction (`CheckMassDelete` counts file-class deletions against tracked files). With A1 landed, the live trigger was the empty-folder-tree rename — legitimately delete + create, 13 dir deletions, guard tripped, daemon wedged. Red-first at three levels (guards unit, engine one-shot, engine `DeferMassDelete`); file mass-deletes still trip regardless of container count. Rows R10 + G4 passing.

Just before it — **W1.8.2 — local directory renames are one remote move, + C1/R18 (2026-07-18).** The most load-bearing change of the workstream, landed red-first with the full suite + `-race` green:

- **The collapse:** a renamed folder is detected from its children's pairing evidence and becomes **one** `MoveRemote` reparent — Drive folder id preserved, zero delete-class actions, one dir-move on every other machine. Implementation insight (decisions.md "W1.8.2 implemented"): once a dir collapses, pass 1 judges its children at their **post-rename paths**, so the per-file moves are never emitted at all. Scatter and empty-dir cases stay delete + create by design; two conservative guards added (N must be a brand-new local dir; N's name free remotely).
- **Both decided traps closed:** the moves-stage split keys on `Type == MoveLocal && IsDir` (the reparent runs after the mkdirs, before the file moves; remote mkdirs under a reparented dir defer behind it — spec §4.5 amended); `moveRemote`'s commit is now `RenameItemPath` + a Drive-fields-only update — no stat, no restated local truth, `is_dir` preserved — closing **C1/R18** (the silent-divergence corruption) in the same shape.
- **Rewrite-under-N works:** children added or edited remotely during the rename land under the new name — no zombie source dir, no duplicate upload.
- Rows R9 (6 reconcile + 2 engine cases) and R18 passing; **MANUAL.md retired two Known-bugs entries** in the same commit per the manual rule.

Earlier the same day: **W1.8.1** (the local-write gate + R13), **MANUAL.md created + its sync rule**, README/MANUAL consolidation, and **adversarial code round 3 + the W1.9 plan.** Five reproduced defects, two inspection items — full check report in [decisions.md](decisions.md) 2026-07-18 "W1.9". The short version:

- **C1 (critical): `moveRemote`'s commit is a sixth unguarded Record.** It stamps the *scanned* md5 onto the destination's *current* stat; a mid-cycle edit → poisoned baseline → local "v2", Drive "v1", every later cycle plans nothing, reported as success. The R6 class, missed at one more site. **Folded into W1.8.2** (same lines as the `IsDir` trap): the commit never restates local truth. Test R18.
- **C2 (high): cross-tree fold collisions unhandled** — fold comparison existed only among remote siblings. Reproduced: local `Readme.txt` vs remote `README.txt` → a permanent case-duplicate minted on Drive, then the user's file *quarantined*; the §4.2 conflict never fired. Decided: targeted fold-match (fold-keyed sibling index fires the ordinary adopt/conflict; fold-shadowed baseline rows are skips, never remote-deletes). W1.9.1, test R19.
- **C3 (medium): a remotely-deleted folder holding a local `.DS_Store` wedges** — `directory not empty`, every cycle, forever. W1.9.2 (ignored/temp leftovers travel to quarantine with the dir), test R20.
- **C4/C5 (low): same-day quarantine collision silently overwrites the earlier rescue copy** (W1.9.3, R21); **type clashes error-loop and mint a same-name pair on Drive** (W1.9.4, R22).
- **C6/C7 (inspection): PKCE added to the OAuth flow** (public client secret makes it load-bearing — W1.9.5, R23); **spec §5's "ignored always reported" promise narrowed** to match the deliberate behavior (done, spec-side, no code).

Also verified clean: path traversal, symlinks, token/socket/DB perms, SQL parameterization, retry/backoff, fsync protocol, add-only repair — and the work-matches-plan check (R1–R8/G3/W2 all where the docs claim; no W1.8 work started early). Three of the five reproduced defects are unguarded-commit/-destination variants — the class W1.8.1's gate exists to end.

Earlier the same day — the **plan review** (five wording defects in W1.8/W4 corrected: R12/oracle scoped to the concurrent transfer stage, A1's §4.4 edge closed as rewrite-under-N, gate ban list extended), **adversarial round 2 of the engine** (nine defects A1–A9, W1.8 planned, W3↔W4 swapped), and **W2 closed**. All in decisions.md.

Docs updated this session: spec §3/§4.2/§5(×2)/§7/§9/§16.1/§16.5; plan.md (W1.8.2 amended, **W1.9 added**, execution order, W4 op menu); testing.md (R18 + R19–R23); decisions.md (one entry).

## In progress

Nothing in flight. Docs committed on master (**not pushed** — Max pushes). No code changes pending.

## Next

**W1.8 — adversarial correctness fixes, round 2** ([plan.md](plan.md) W1.8), **in the listed order**. Items 1–6 are **done**. **Next is item 7 — [A6] the daemon never exits on a watcher-rebuild failure** (`watch.go` returns the error from `startNotifier` at the rebuild site, killing the daemon — against `Run`'s own doc comment, spec §8.1 and §10; the `pollingOnly` retry branch handles the identical error by degrading, which is the tell; code fix only, spec already correct; test R15 — the fix's commit also retires the W1.8.7 Known-bugs entry from MANUAL.md). Then item 8 (A9 + A8). Item 9's remainder is one small named test. Discipline is W1.7's: **red-first regression test, then the fix, then the testing.md row**, suite + `-race` green at every commit.

Every design question is closed — including, as of the plan review, the §4.4 edge W1.8.2 previously left to "check": remote-side rows under a locally renamed dir resolve under the new name. plan.md carries the decided approach inline, decisions.md carries the reasoning and the rejected alternatives. **Implement, don't re-derive** — and honor the two scoping corrections (R12 and the W4 oracle cover the concurrent transfer stage only). If something in the plan turns out to be wrong when it meets the code, that is a decisions.md entry, not a silent deviation (W1.6 is the precedent: the analysis was wrong, the test proved it, and the correction was recorded).

Then **W1.9** — adversarial round-3 fixes ([plan.md](plan.md) W1.9): five items in listed order — cross-tree fold conflicts (C2/R19), the quarantine sweep (C3/R20), rescue-copy protection (C4/R21), type-clash skips (C5/R22), PKCE (C6/R23). Note W1.8.2 now also carries C1/R18 (the move commit never restates local truth).

Then **W4** (fuzzer, with the strengthened oracle — its op menu now includes the round-3 classes), then **W3** (fswatch + FSEvents + 50k scale + soak re-run).

## Blockers / parked

- **W6** (real multi-machine rollout): needs a second physical machine — Max's step.
- **Optional, Max:** rotate the OAuth client in the Google console before publication (hygiene; the decided credential model doesn't require it).
- **Deferred to publication:** the donation note (W2.4d; lands in MANUAL.md §5 since the README/MANUAL consolidation) — repo is still private.
- **Carried into W3 to size, not a defect:** every cycle that writes files fires its own fsnotify events, re-triggering the loop. Converges and is harmless at personal scale; measure under the W1-scale test before deciding whether a backend should suppress self-inflicted events.
