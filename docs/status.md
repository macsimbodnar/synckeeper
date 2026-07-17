# Status — session-to-session state pointer

The single answer to "where are we?". Read this first; **rewrite it before ending any work session** (sections replaced in place — history lives in `git log` and [decisions.md](decisions.md), not here).

**Updated:** 2026-07-17 — W1.4 session.

## Last completed

**W1.4 / R4 — download overwrite window closed** (2026-07-17). Downloads carry the plan's assumption about the target (`LocalExists` + scanned size/mtime, or absent); the executor re-stats immediately before the atomic rename and refuses on any drift, so a local write racing the cycle wins it and becomes an ordinary conflict next cycle. Tests at executor (both drift directions) and engine level (mid-cycle edit → conflict copy, never loss). Suite + `-race` green. Decision entry: decisions.md 2026-07-17 "R4 fix". Earlier today: R3 (`9480788`), R2 (`18b37f4`), R1 (`25294ff`), docs restructure (`1dfb2a1`), design overhaul (`17d2fa6`).

## In progress

W1 (correctness fixes) — items 1–4 of 6 done.

## Next

**W1.5 — read-only commands must not migrate the DB** ([plan.md](plan.md) W1.5, testing.md R5, spec §14):

1. Regression test first: `openReadEnv` (used by `status`/`activity`/`config`/`account`) calls `statedb.Open`, which runs migrations **without the instance lock** — a newer binary's read command upgrades the schema under an older running daemon. Test: a DB at an older schema version opened via the read path must stay at its version; a schema *newer* than the binary must be refused politely (already handled in `migrate`); reads against the current version work.
2. Fix: a read-only open in `statedb` (e.g. `OpenRead`) that checks the schema version and refuses to migrate — behind/ahead both produce a clear "run a state-changing command / rebuild" error; `cmd/synckeeper/readenv.go` switches to it.
3. Suite green → check off in plan.md → rewrite this file.

Then W1.6 (swap-rename sweep) closes W1.

## Blockers / parked

- **W6** (real multi-machine rollout): needs a second physical machine — Max's step.
- **Optional, Max:** rotate the OAuth client in the Google console before publication (hygiene; the decided credential model doesn't require it).
- Pre-existing gofmt drift in 4 untouched files (`cmd/synckeeper/status.go`, `internal/config/config.go`, `internal/driveclient/driveclient.go`, `internal/service/status_test.go`) — fold into W2 hygiene.
