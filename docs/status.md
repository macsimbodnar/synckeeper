# Status — session-to-session state pointer

The single answer to "where are we?". Read this first; **rewrite it before ending any work session** (sections replaced in place — history lives in `git log` and [decisions.md](decisions.md), not here).

**Updated:** 2026-07-17 — W1.5 session.

## Last completed

**W1.5 / R5 — read path can no longer migrate the DB** (2026-07-17). New `statedb.OpenRead`: exact schema-version match only (older → guidance to run `sync` once with this binary; newer → binary too old; missing DB never created); `openReadEnv` switched to it. Migrations now run only under the instance lock (spec §14). Test: `TestR5OpenReadNeverMigrates`. Suite + `-race` green. Earlier today: R4 (`5751812`), R3 (`9480788`), R2 (`18b37f4`), R1 (`25294ff`), docs restructure (`1dfb2a1`), design overhaul (`17d2fa6`).

## In progress

W1 (correctness fixes) — items 1–5 of 6 done.

## Next

**W1.6 — swap-rename sweep** ([plan.md](plan.md) W1.6, testing.md R6) — the last W1 item, verification-only unless it surprises:

1. Write the test: same-cycle cross-rename of two files (`a.txt`↔`b.txt` swapped remotely) — the 2026-07-17 review's analysis says it self-heals within a bounded number of cycles (transient unique-constraint / rename failures, no loss) because both contents stay on Drive.
2. If the test confirms bounded convergence: mark R6 passing, document as accepted noise in the plan item, done. If it diverges or loses content: it becomes a real fix, scoped then.
3. Suite green → check off in plan.md (closing W1) → rewrite this file pointing at W2.

## Blockers / parked

- **W6** (real multi-machine rollout): needs a second physical machine — Max's step.
- **Optional, Max:** rotate the OAuth client in the Google console before publication (hygiene; the decided credential model doesn't require it).
- Pre-existing gofmt drift in 4 untouched files (`cmd/synckeeper/status.go`, `internal/config/config.go`, `internal/driveclient/driveclient.go`, `internal/service/status_test.go`) — fold into W2 hygiene.
