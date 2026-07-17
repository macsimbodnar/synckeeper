# Status — session-to-session state pointer

The single answer to "where are we?". Read this first; **rewrite it before ending any work session** (sections replaced in place — history lives in `git log` and [decisions.md](decisions.md), not here).

**Updated:** 2026-07-17 — W1.1 session.

## Last completed

**W1.1 / R1 — the data-loss ordering bug is fixed** (2026-07-17). A true conflict no longer routes local content through a `MoveLocal`: the conflict backup acts on the file's current local path, and conflict transfers carry `ProtectedBy`, which the executor refuses to run when the named backup failed (invariant 7 / spec §4.5). Regression tests at three levels — engine (`TestR1RemoteMoveEditVsLocalEditConflict`), reconcile (`TestRemoteMovePlusConflictBacksUpFromCurrentPath`), executor (`TestProtectedDownloadRefusedWhenBackupFails`); full suite and `-race` green. Decision entry: decisions.md 2026-07-17 "R1 fix". Before that: docs restructure (`1dfb2a1`), design overhaul (`17d2fa6`).

## In progress

W1 (correctness fixes) — item 1 of 6 done.

## Next

**W1.2 — fix R2** (the livelock), same TDD order:

1. Regression test first ([testing.md](testing.md) R2 scenario: remote same-id dir rename + new remote subdir inside it — currently `MkdirLocal` runs before moves, `MkdirAll` creates the move destination, `MoveLocal` fails `file exists` every cycle; must FAIL against current code).
2. Fix per spec §4.5: order local dir moves ahead of local mkdirs (preferred), or make `moveLocal` merge into plan-created empty scaffold. See [plan.md](plan.md) W1.2.
3. Suite green → check off in plan.md → rewrite this file.

Then W1.3–W1.6 strictly in order.

## Blockers / parked

- **W6** (real multi-machine rollout): needs a second physical machine — Max's step.
- **Optional, Max:** rotate the OAuth client in the Google console before publication (hygiene; the decided credential model doesn't require it).
