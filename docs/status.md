# Status — session-to-session state pointer

The single answer to "where are we?". Read this first; **rewrite it before ending any work session** (sections replaced in place — history lives in `git log` and [decisions.md](decisions.md), not here).

**Updated:** 2026-07-18 — adversarial round 3, over the **code** (the third code round: W1.7 → W1.8 → this). **W1.8 still blocks everything else; W1.9 is new and runs right after it.** No production code changed this session: five defects were reproduced with throwaway probes (deleted, tree clean), decided, and planned.

## Last completed

**Adversarial code analysis round 3 + its fix plan (2026-07-18).** Five reproduced defects, two inspection items — full check report in [decisions.md](decisions.md) 2026-07-18 "W1.9". The short version:

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

**W1.8 — adversarial correctness fixes, round 2** ([plan.md](plan.md) W1.8): eight open items (item 9's doc half is done; its remainder is one small named test), **in the listed order**. Item 1 is the local-write gate, because everything after it is written against that gate; items 2–3 (directory renames as moves, guard counts files) are the highest user-visible impact. Discipline is W1.7's: **red-first regression test, then the fix, then the testing.md row**, suite + `-race` green at every commit.

Every design question is closed — including, as of the plan review, the §4.4 edge W1.8.2 previously left to "check": remote-side rows under a locally renamed dir resolve under the new name. plan.md carries the decided approach inline, decisions.md carries the reasoning and the rejected alternatives. **Implement, don't re-derive** — and honor the two scoping corrections (R12 and the W4 oracle cover the concurrent transfer stage only). If something in the plan turns out to be wrong when it meets the code, that is a decisions.md entry, not a silent deviation (W1.6 is the precedent: the analysis was wrong, the test proved it, and the correction was recorded).

Then **W1.9** — adversarial round-3 fixes ([plan.md](plan.md) W1.9): five items in listed order — cross-tree fold conflicts (C2/R19), the quarantine sweep (C3/R20), rescue-copy protection (C4/R21), type-clash skips (C5/R22), PKCE (C6/R23). Note W1.8.2 now also carries C1/R18 (the move commit never restates local truth).

Then **W4** (fuzzer, with the strengthened oracle — its op menu now includes the round-3 classes), then **W3** (fswatch + FSEvents + 50k scale + soak re-run).

## Blockers / parked

- **W6** (real multi-machine rollout): needs a second physical machine — Max's step.
- **Optional, Max:** rotate the OAuth client in the Google console before publication (hygiene; the decided credential model doesn't require it).
- **Deferred to publication:** the README donation note (W2.4d) — repo is still private.
- **Carried into W3 to size, not a defect:** every cycle that writes files fires its own fsnotify events, re-triggering the loop. Converges and is harmless at personal scale; measure under the W1-scale test before deciding whether a backend should suppress self-inflicted events.
