# Status — session-to-session state pointer

The single answer to "where are we?". Read this first; **rewrite it before ending any work session** (sections replaced in place — history lives in `git log` and [decisions.md](decisions.md), not here).

**Updated:** 2026-07-18 — plan-review session (round 3: adversarial review of the **plan itself**, not the code). **W1.8 still blocks everything else.** No code changed this session: five defects in the plan's normative wording were found, decided, and corrected in the docs.

## Last completed

**An adversarial review of the W1.8/W4 plan before implementation (2026-07-18).** The verification half came back clean — all ~20 code line references in W1.8/decisions are accurate at HEAD, both A1 implementation traps are real, `quarantineLocal` is genuinely unguarded, the guard genuinely counts directories, W2's claims are all in the code, suite green, tree clean. Five defects were in the plan's own wording, all corrected (Max's decisions; see [decisions.md](decisions.md) 2026-07-18 "Plan review"):

1. **§4.5 enforcement + W4 structural oracle scoped to the concurrent transfer stage.** The blanket "no two same-stage actions on one rel_path or an ancestor/descendant pair" refuses correct plans — nested mkdirs, bottom-up deletes, and R7's decided backup-then-move are same-stage sequences on overlapping paths *by design*. A literal R12 goes red on S8. Scoped, it still catches A4 mechanically.
2. **W4's identity-stability oracle scoped to renames the pairing evidence supports** (unmodified files; directories with surviving paired children). Unscoped, it flags decided-correct behavior — the empty-dir rename (R9 pins delete + create as intended) and §4.3's unpaired edit-and-rename remainders.
3. **A1's last open edge closed: remote-side rows resolve under N.** A local dir rename concurrent with a remote child add/edit fired the collapse but planned downloads at the dead pre-reparent path — a zombie source dir, the rename half-undone. Rule: the shared `rewrite()` applies to *the side that hasn't moved yet* (local-driven dir moves rewrite remote paths). Rejected alternative: blocking the collapse (keeps the zombie *and* churns the id). R9 gains both cases.
4. **Gate ban list extended** (`os.WriteFile` was the obvious bypass; + `Truncate`/`Chtimes`/`Link`/`Symlink`); the one exemption — `doctor --repair`'s prefix-matched orphan-temp removal, outside the executor package — is recorded in spec §7 rather than left discoverable.
5. **Bookkeeping:** W1.8.9's reconciliation half marked done (it was completed the session the plan was written); its remainder is N3's named case, so the ledger stops vouching for a test that exists only by implication.

Earlier the same day — **adversarial round 2 of the engine** (nine defects A1–A9, W1.8 planned, W3↔W4 swapped; see decisions.md) and **W2 closed** (dead knob, normalization folding, reload hint, BYO credentials, build-policy docs).

Docs updated this session: spec §4.3/§4.5/§7/§16.1/§16.7; plan.md W1.8.1/2/5/9 + W4.2; testing.md R9/R12/N3; decisions.md (one entry).

## In progress

Nothing in flight. Docs committed on master (**not pushed** — Max pushes). No code changes pending.

## Next

**W1.8 — adversarial correctness fixes, round 2** ([plan.md](plan.md) W1.8): eight open items (item 9's doc half is done; its remainder is one small named test), **in the listed order**. Item 1 is the local-write gate, because everything after it is written against that gate; items 2–3 (directory renames as moves, guard counts files) are the highest user-visible impact. Discipline is W1.7's: **red-first regression test, then the fix, then the testing.md row**, suite + `-race` green at every commit.

Every design question is closed — including, as of the plan review, the §4.4 edge W1.8.2 previously left to "check": remote-side rows under a locally renamed dir resolve under the new name. plan.md carries the decided approach inline, decisions.md carries the reasoning and the rejected alternatives. **Implement, don't re-derive** — and honor the two scoping corrections (R12 and the W4 oracle cover the concurrent transfer stage only). If something in the plan turns out to be wrong when it meets the code, that is a decisions.md entry, not a silent deviation (W1.6 is the precedent: the analysis was wrong, the test proved it, and the correction was recorded).

Then **W4** (fuzzer, with the strengthened oracle), then **W3** (fswatch + FSEvents + 50k scale + soak re-run).

## Blockers / parked

- **W6** (real multi-machine rollout): needs a second physical machine — Max's step.
- **Optional, Max:** rotate the OAuth client in the Google console before publication (hygiene; the decided credential model doesn't require it).
- **Deferred to publication:** the README donation note (W2.4d) — repo is still private.
- **Carried into W3 to size, not a defect:** every cycle that writes files fires its own fsnotify events, re-triggering the loop. Converges and is harmless at personal scale; measure under the W1-scale test before deciding whether a backend should suppress self-inflicted events.
