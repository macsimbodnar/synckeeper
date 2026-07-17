# Status — session-to-session state pointer

The single answer to "where are we?". Read this first; **rewrite it before ending any work session** (sections replaced in place — history lives in `git log` and [decisions.md](decisions.md), not here).

**Updated:** 2026-07-17 — W1.7 session (adversarial analysis). **W1 reopened and re-closed as W1.7.**

## Last completed

**W1.7 — adversarial-analysis correctness fixes: R7, R8, G3 done (2026-07-17).** An adversarial re-analysis of the engine against the core invariant found a **confirmed critical data-loss bug the W1 review missed**: `MoveLocal` (a third local-overwriting primitive alongside the download and record that W1 guarded) `os.Rename`d over an untracked local file when a remote rename/move landed on its name — silent loss, reported as a clean success. Fixed with the R4 pattern generalized to moves (reconcile preserves an untracked occupant as a conflict copy + stat-pins a tracked one; executor re-stats the destination and refuses an unexpected occupant). R8 gave `ConflictBackup` the same guard. G3 (Finding 3) made the daemon **defer** a mass delete — sync everything else, surface the block — matching spec §6/§8.1 instead of aborting the whole cycle (new `Options.DeferMassDelete`). Red-first tests at three levels; suite + `-race` green. See decisions.md 2026-07-17 "W1.7 (R7/R8/G3)".

Prior W1 (2026-07-17): R1 conflict ordering, R2 dir-rename livelock, R3 stale mirror on `init --force`, R4 download overwrite window, R5 read-path migrations, R6 record poisoning.

## In progress

Nothing in flight. **W1.7 changes are staged but not yet committed** (per the never-push rule, Max commits/pushes). Suggested commits: one for R7/R8 (reconcile + executor + engine + tests + spec §4.5/§7), one for G3/C1 (engine + watch + tests + spec §6). Next workstream is W2.

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
