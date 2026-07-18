# Status — session-to-session state pointer

The single answer to "where are we?". Read this first; **rewrite it before ending any work session** (sections replaced in place — history lives in `git log` and [decisions.md](decisions.md), not here).

**Updated:** 2026-07-18 — W2 session. **W2 is closed.** (W1.7 correctness fixes closed earlier the same run.)

## Last completed

**W2 — spec alignment & hygiene: all five items done (2026-07-18), committed on master.**

1. **W2.1** (`3f91f1d`) removed the dead `full_rescan_interval_secs` knob (config, default, validation, reload copy, test TOML).
2. **W2.2** (`4b7cbf0`) Unicode normalization folding: `names.NormalizationInsensitiveFS` probe + `names.FoldKey` (NFC→lower→NFC) combined case/normalization fold in `remotedelta.Snapshot`; the fold reports its cause (case / normalization / both); `golang.org/x/text/unicode/norm` promoted to a direct dep; N2 passing, probe validated on real APFS.
3. **W2.3** (`791b7a6`) refreshed the stale `config` reload hint (points at `synckeeper reload`) and cleared the parked gofmt drift.
4. **W2.4** (`49c5f7f`) BYO OAuth client via `credentials.json` (config-dir → embedded; the config-keys tier was dropped — it would leak the secret via `config`; see decisions.md); `account` now shows the active client + id; stray `client_secret_*.json` removed, `.gitignore` blocks BYO files; README "Credentials" section.
5. **W2.5** (`48e1531`) README/Makefile build-policy consistency (Go 1.26+ per go.mod; native `build` vs pure-Go-only legacy `build-all`).

Earlier this run — **W1.7** (`adfb122`): R7 critical `MoveLocal`/`ConflictBackup` overwrite data-loss guard + G3 daemon defers a mass delete. Prior W1: R1–R6.

## In progress

Nothing in flight. All work committed on master (**not pushed** — Max pushes).

## Next

**W3 — Watcher modularization + FSEvents** ([plan.md](plan.md) W3): extract the `fswatch` module interface from `internal/watch` (fsnotify becomes one backend, poll-only the universal fallback), then the FSEvents implementation (first cgo in the repo), then the ≥50k-file scale test. This is where `CGO_ENABLED=0` and `build-all` get retired for native builds (spec §10).

## Blockers / parked

- **W6** (real multi-machine rollout): needs a second physical machine — Max's step.
- **Optional, Max:** rotate the OAuth client in the Google console before publication (hygiene; the decided credential model doesn't require it).
- **Deferred to publication:** the README donation note (W2.4d) — repo is still private.
