# Status — session-to-session state pointer

The single answer to "where are we?". Read this first; **rewrite it before ending any work session** (sections replaced in place — history lives in `git log` and [decisions.md](decisions.md), not here).

**Updated:** 2026-07-17 — docs-restructure session.

## Last completed

1. **Full code review with runnable repros** (2026-07-17): engine matches the contract; two ordering bugs confirmed by failing tests — R1 (conflict-backup/move ordering → real data loss) and R2 (mkdir-before-move → permanent livelock) — plus gaps R3–R6, N2. All captured as [testing.md](testing.md) rows and [plan.md](plan.md) W1 items.
2. **Design overhaul** (commit `17d2fa6`): [spec.md](spec.md) rewritten — platform-agnostic, daemon-first, durability invariant 7 (dependency-aware ordering), OS-module contract (fswatch/FSEvents etc.), native-build policy (cgo allowed), credential model (embedded defaults + BYO override), Dropbox reference appendix, acceptance criteria §16. [plan.md](plan.md) restructured into workstreams W1–W9.
3. **Docs restructured as inter-agent memory** (this session): `CLAUDE.md`, this file, phases archived to [history/](history/), decision-log format gains "decided by".

## In progress

Nothing in flight.

## Next

**W1.1 — fix R1** (the data-loss bug), in this order:

1. Recreate the repro as regression test R1 (scenario spelled out in [testing.md](testing.md) → "Review regressions"; it must FAIL against current code).
2. Fix per spec §4.5: conflict backup taken from the file's *current* local path when the remote also moved it; a download must not run if its protecting backup failed.
3. Full suite green, then check the item off in [plan.md](plan.md) W1 with the date.

Then W1.2 (R2 livelock) and the rest of W1 strictly in order. W1 blocks all other workstreams.

## Blockers / parked

- **W6** (real multi-machine rollout): needs a second physical machine — Max's step.
- **Optional, Max:** rotate the OAuth client in the Google console before publication (historical secret is in git history; rotation is hygiene, not required by the decided credential model).
- The review's repro test code lived in a session scratchpad; if lost, recreate from the testing.md R1/R2 scenario descriptions (they are complete).
