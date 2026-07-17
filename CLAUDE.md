# Synckeeper — agent guide

Personal bidirectional sync between one local folder and one Google Drive folder, in Go. Daemon-first. The one rule that outranks everything: **the engine must never silently lose or corrupt a file.**

## Start here, in this order

1. **[docs/status.md](docs/status.md)** — last / current / next task. The session-to-session state pointer; **update it before ending any work session**.
2. **[docs/plan.md](docs/plan.md)** — the workstream backlog (W1–W9) that status points into.
3. **[docs/spec.md](docs/spec.md)** — the authoritative design doc & contract for whatever you touch. When spec and plan disagree, the spec wins.
4. **[docs/decisions.md](docs/decisions.md)** — before changing anything already decided: what was chosen, when, by whom, and why. Newest first.

## Doc map (this is the project's permanent memory — keep it alive)

| File | Role | Update when |
|---|---|---|
| `docs/status.md` | last/current/next pointer | end of every work session (rewrite sections, don't append) |
| `docs/spec.md` | design doc & contract, acceptance criteria (§16) | behavior changes — same commit, dated inline note |
| `docs/plan.md` | workstreams W1–W9 + retired phase history | item done / reprioritized (date it) |
| `docs/decisions.md` | append-only log: context, decision, who, consequences | every scope change / deviation / non-obvious choice, before or alongside the code |
| `docs/testing.md` | acceptance ledger — rows are the criteria | with the feature, never after |
| `docs/ideas.md` | Max's informal wishlist / inbox | triage items into spec + plan |
| `docs/history/` | retired phase docs 0–7 (the pre-2026-07-17 phase system) | never — read-only archive |
| `README.md` | human build/run/test instructions | same change that alters a command or Makefile target |

## Project state model

- The **phase system (0–7) is finished and retired** — those docs live in `docs/history/` and are context, not work. Do not resume phase checklists.
- Active work is organized as **workstreams W1–W9** in `docs/plan.md`. W1 (correctness fixes) blocks everything else.
- Decisions belong to **Max**; agents propose, `decisions.md` records who/when/why.

## Hard rules

- **Never `git push`.** Commit when asked or when a work item completes; Max pushes himself.
- `go build ./... && go vet ./... && go test ./...` green at every commit.
- Builds are **native per platform** (no cross-compile requirement); cgo is permitted where the OS's best API needs it (spec §10). Pure Go preferred where equal.
- Tests land with the feature, as `docs/testing.md` rows.
- Durability invariants (spec §3) are non-negotiable; invariant 7 (dependency-aware plan ordering) exists because ordering bugs shipped once — see decisions.md 2026-07-17.
- The credential model (spec §9): Max's OAuth client credentials stay embedded as the default; never "fix" that by removing them.

## Commands

```sh
make test                              # full offline suite (in-memory Drive fake)
SYNCKEEPER_LIVE_TEST=1 go test ./...   # live smoke vs a throwaway Drive folder
SYNCKEEPER_SOAK_SECONDS=7200 go test ./internal/watch/ -run TestSoak -timeout 3h
```
