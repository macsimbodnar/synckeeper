# Status — session-to-session state pointer

The single answer to "where are we?". Read this first; **rewrite it before ending any work session** (sections replaced in place — history lives in `git log` and [decisions.md](decisions.md), not here).

**Updated:** 2026-07-17 — W1.3 session.

## Last completed

**W1.3 / R3 — `init --force` stale mirror fixed** (2026-07-17). `initialize` now calls `remotedelta.ForceFullWalk` (fresh page token + full mirror rebuild in one step) instead of only resetting the token, so remote changes made before a re-init can no longer be silently skipped; a fresh `init` gets a pre-warmed mirror as a side benefit. Test: `TestR3ForceReinitSeesPriorRemoteChanges` (cmd). Suite green. Earlier today: R2 fix (`18b37f4`), R1 fix (`25294ff`), docs restructure (`1dfb2a1`), design overhaul (`17d2fa6`).

## In progress

W1 (correctness fixes) — items 1–3 of 6 done.

## Next

**W1.4 — download overwrite window** ([plan.md](plan.md) W1.4, testing.md R4, spec §7):

1. Regression test first: a local edit landing *between the scan and the download's rename* must not be overwritten. Deterministic hook exists: `executor.FaultHook` at checkpoint `CPDownloadTempWritten` (temp complete, target untouched) can modify the target file mid-download.
2. Fix per spec §7: before the atomic rename, re-stat the target; if size/mtime differ from what the cycle's scan recorded, abandon + requeue (the local edit wins this round, next cycle reconciles). Requires the download action to carry the scanned stat (plumb via `reconcile.Action` — e.g. expected local size/mtime — or via the executor's items snapshot).
3. Suite green → check off in plan.md → rewrite this file.

Then W1.5 (read-path migrations without the lock), W1.6 (swap-rename sweep).

## Blockers / parked

- **W6** (real multi-machine rollout): needs a second physical machine — Max's step.
- **Optional, Max:** rotate the OAuth client in the Google console before publication (hygiene; the decided credential model doesn't require it).
- Pre-existing gofmt drift in 4 untouched files (`cmd/synckeeper/status.go`, `internal/config/config.go`, `internal/driveclient/driveclient.go`, `internal/service/status_test.go`) — fold into W2 hygiene.
