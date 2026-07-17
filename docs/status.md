# Status — session-to-session state pointer

The single answer to "where are we?". Read this first; **rewrite it before ending any work session** (sections replaced in place — history lives in `git log` and [decisions.md](decisions.md), not here).

**Updated:** 2026-07-17 — W1.6 session. **W1 is closed.**

## Last completed

**W1 — correctness fixes: all six items done (2026-07-17).** The finale, W1.6/R6, was planned as verification-only but disproved the review's "swap self-heals" analysis: an unprotected `Record` could stamp a planned md5 onto the wrong file after half-failed moves, poisoning the baseline into permanent silent divergence. Fixed by generalizing the invariant-7 mechanisms — `ProtectedBy` now covers local moves, and every file-level `Record` verifies the scanned size/mtime before overwriting the baseline's truth. See decisions.md 2026-07-17 "R6" (which formally corrects the earlier claim). W1 summary: R1 data-loss conflict ordering (`25294ff`), R2 dir-rename livelock (`18b37f4`), R3 stale mirror on `init --force` (`9480788`), R4 download overwrite window (`5751812`), R5 read-path migrations (`d2c35c9`), R6 record poisoning (this commit). All with red-first regression tests at up to three levels; suite + `-race` green throughout.

## In progress

Nothing in flight. W1 `done`; next workstream is W2.

## Next

**W2 — spec alignment & hygiene** ([plan.md](plan.md) W2), in order:

1. **W2.1** Remove the dead `full_rescan_interval_secs` knob (config, validation, reload, docs) — spec §13.
2. **W2.2** Unicode normalization folding (NFC/NFD collapse in `remotedelta.Snapshot` + probe alongside `names.CaseInsensitiveFS`) — spec §5, testing.md N2. Needs `golang.org/x/text/unicode/norm` (pure Go).
3. **W2.3** Stale text in `cmd/synckeeper/config.go` + the parked gofmt drift in 4 files (see below).
4. **W2.4** Credentials: BYO override lookup (config-dir `credentials.json` → config keys → embedded), `account` reports which is active, README BYO page; drop the stray `client_secret_*.json` from the repo root.
5. **W2.5** README build-policy update (native builds; `build-all` marked legacy).

W2.1 and W2.3 are trivial; W2.2 is the substantial one; W2.4 touches auth plumbing.

## Blockers / parked

- **W6** (real multi-machine rollout): needs a second physical machine — Max's step.
- **Optional, Max:** rotate the OAuth client in the Google console before publication (hygiene; the decided credential model doesn't require it).
- gofmt drift in 4 pre-existing files (`cmd/synckeeper/status.go`, `internal/config/config.go`, `internal/driveclient/driveclient.go`, `internal/service/status_test.go`) — clean up in W2.3.
