# Status — session-to-session state pointer

The single answer to "where are we?". Read this first; **rewrite it before ending any work session** (sections replaced in place — history lives in `git log` and [decisions.md](decisions.md), not here).

**Updated:** 2026-07-21 — **W1.8 and W1.9 complete; a round-4 adversarial review added W1.9.6.** Every reproduced defect from all four adversarial rounds is fixed; MANUAL's Known-bugs list is down to one narrowed entry (fold-equal *folders*), owned by the recorded gate-directory-arm follow-up. **Next workstream: W4** (the fuzzer with the strengthened oracle), then W3.

## Last completed

**W1.9.6 — a shadowed folder's tracked subtree is held harmless (2026-07-21).** An adversarial review of docs/code/tests found that W1.9.1's "never a quarantine" guarantee held only for the directly-colliding row: `remotedelta.Snapshot` skips a folder that lost "first by id" without walking its subtree, so the engine's shadowed set missed the folder's tracked descendants and reconcile quarantined them off disk (reproduced end to end, R24 — content recoverable, but the stated bound was wrong, and untested). Fix is engine-side and small: `expandShadowed` marks every baseline row under a shadowed skip's rel_path, not just the directly-skipped id. Not fold-specific (the exact-duplicate-name folder case shadows identically on any FS). R24 + `TestExpandShadowedCoversSubtree` passing; suite + `-race` green. The full directory arm stays deferred; this only closes the descendant-quarantine hole in its safety envelope. The same review also corrected MANUAL.md's stale "Last updated" stamp (2026-07-18 → 2026-07-21) and otherwise found docs/code/tests sound (build/vet/test/`-race` all green; every testing.md row's named test verified to exist and, on spot-check, to assert its claim).

**W1.9.5 — PKCE on the OAuth loopback flow, and W1.9 closes (2026-07-19).** `GenerateVerifier` + `S256ChallengeOption` on the URL, `VerifierOption` on the exchange. R23 runs the whole loopback flow in-process against a fake token endpoint (two seams: endpoint + browser-opener; the test plays the browser) and proves S256(verifier) equals the URL's promised challenge — a URL-only unit test was rejected as it wouldn't catch a verifier that never reaches the exchange (decisions.md "W1.9.5"). Row R23 passing.

**The 2026-07-19 run, in one paragraph** (eleven commits, `ac71dd4` → `f63b202`; each fix has a full commit message, a decisions.md entry for its non-obvious choices, and a passing testing.md row): W1.8 items 5–9 — §4.5 enforcement (`ValidateTransferStage`, R12, plus a follow-up commit closing two review-found doc nits), the reload race (atomic ignore-glob snapshot, R14), watcher-failure survival at rebuild *and* launch (R15), the crashed-dir-move replay + `Snapshot` visited set (R16/R17, where the red test corrected the round-2 analysis — the crash shape had been destructive, not benign) and N3's named case — then W1.9 items 1–5 — cross-tree fold conflicts for files (R19; **dir arm deferred, recorded**), the quarantine leftover sweep (R20), rescue-copy uniquification (R21), type-clash skips in every direction (R22), PKCE (R23). Four MANUAL Known-bugs entries retired, one narrowed. Standing follow-up from the run: **the gate's directory arm** (decisions.md "W1.9.1"/"W1.8.8").

The 2026-07-18 session (gate + A1 collapse + guard fix + rounds 2–3 analysis + W2 close + MANUAL creation) is fully recorded in decisions.md's entries of that date and the git history; the round-3 defect list (C1–C7) lives in decisions.md 2026-07-18 "W1.9".

## In progress

Nothing in flight. Docs committed on master (**not pushed** — Max pushes). No code changes pending.

## Next

**W4 — randomized sync testing** ([plan.md](plan.md) W4), the fuzzer that simulates N machines against the in-memory fake and asserts convergence, durability, and the strengthened oracle: (a) identity stability **scoped to renames the pairing evidence supports** (plan review 2026-07-18 — unscoped it flags decided-correct behavior: empty-dir renames, §4.3 remainders); (b) the §4.5 structural invariant via `reconcile.ValidateTransferStage` (shared with R12, concurrent transfer stage only). The op menu must include the classes that actually shipped bugs: R7 move-onto-occupied, R6 swaps, A4 new-under-moved-dir, A1 dir renames both sides, C2 fold collisions, C5 type clashes, C4 delete/recreate churn (plan.md W4.3). W1.9's dir-arm follow-up stays unscheduled and must not silently leak into W4's oracle expectations (fold-equal *folder* creation is a known, recorded gap). A fuzzer-found defect follows the same discipline as ever: **minimized red regression test, then the fix, then the testing.md row**, suite + `-race` green at every commit — and a MANUAL.md Known-bugs entry if it ships unfixed.

W4's design decisions are recorded (decisions.md 2026-07-18 "Roadmap" and the plan review; plan.md W4.1–W4.4). **Implement, don't re-derive** — and honor the two oracle scopings. If something in the plan turns out to be wrong when it meets the code, that is a decisions.md entry, not a silent deviation (W1.6 set the precedent; W1.8.8 repeated it).

Then **W3** (fswatch + FSEvents + 50k scale + soak re-run), then W5. Unscheduled, on the table from the W1.8/W1.9 close-outs: **the gate's directory arm** (case-only dir renames + dir fold-adopts + R7-for-dirs + tracked case-only renames — one family, see decisions.md "W1.9.1" and "W1.8.8").

## Blockers / parked

- **W6** (real multi-machine rollout): needs a second physical machine — Max's step.
- **Optional, Max:** rotate the OAuth client in the Google console before publication (hygiene; the decided credential model doesn't require it).
- **Deferred to publication:** the donation note (W2.4d; lands in MANUAL.md §5 since the README/MANUAL consolidation) — repo is still private.
- **Carried into W3 to size, not a defect:** every cycle that writes files fires its own fsnotify events, re-triggering the loop. Converges and is harmless at personal scale; measure under the W1-scale test before deciding whether a backend should suppress self-inflicted events.
