# Status — session-to-session state pointer

The single answer to "where are we?". Read this first; **rewrite it before ending any work session** (sections replaced in place — history lives in `git log` and [decisions.md](decisions.md), not here).

**Updated:** 2026-07-17 — W1.2 session.

## Last completed

**W1.2 / R2 — the dir-rename livelock is fixed** (2026-07-17). Plan stage order is now dir-moves → mkdirs → file-moves/backups → transfers → deletes (spec §4.5 updated in the same change): a `MkdirLocal` can no longer scaffold a pending dir move's destination, and `MkdirRemote` under a remotely moved dir now resolves its parent id first try. Tests: `TestR2RemoteDirRenameWithNewSubdir` (engine), `TestDirMoveOrdersBeforeMkdirLocal` (reconcile); suite + `-race` green. Decision entry: decisions.md 2026-07-17 "R2 fix". Before that: R1 fix (`25294ff`), docs restructure (`1dfb2a1`), design overhaul (`17d2fa6`).

## In progress

W1 (correctness fixes) — items 1–2 of 6 done.

## Next

**W1.3 — `init --force` leaves a stale remote mirror** ([plan.md](plan.md) W1.3, testing.md R3):

1. Regression test first: after `init --force`, a remote change made *before* the re-init must still reach the local side (currently the fresh page token skips it and the stale `remote_nodes` mirror hides it — the divergence is silent).
2. Fix per spec §12: `--force` performs the same forced full walk as `doctor --repair` (`remotedelta.ForceFullWalk`) instead of only resetting the page token.
3. Suite green → check off in plan.md → rewrite this file.

Then W1.4 (download overwrite window), W1.5 (read-path migrations), W1.6 (swap-rename sweep).

## Blockers / parked

- **W6** (real multi-machine rollout): needs a second physical machine — Max's step.
- **Optional, Max:** rotate the OAuth client in the Google console before publication (hygiene; the decided credential model doesn't require it).
- Pre-existing gofmt drift in 4 untouched files (`cmd/synckeeper/status.go`, `internal/config/config.go`, `internal/driveclient/driveclient.go`, `internal/service/status_test.go`) — fold into W2 hygiene.
