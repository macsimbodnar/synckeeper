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
| `docs/worklog.md` | append-only action log, keyed by the prompt | every prompt — append the prompt + timestamp + short action/decision bullets (never rewrite past entries) |
| `docs/spec.md` | design doc & contract, acceptance criteria (§16) | behavior changes — same commit, dated inline note |
| `docs/plan.md` | workstreams W1–W9 + retired phase history | item done / reprioritized (date it) |
| `docs/decisions.md` | append-only log: context, decision, who, consequences | every scope change / deviation / non-obvious choice, before or alongside the code |
| `docs/testing.md` | acceptance ledger — rows are the criteria | with the feature, never after |
| `docs/ideas.md` | Max's informal wishlist / inbox | triage items into spec + plan |
| `docs/history/` | retired phase docs 0–7 (the pre-2026-07-17 phase system) | never — read-only archive |
| `README.md` | repo landing page: pitch, doc map, build/test instructions — **deliberately no usage sections** (those live in `MANUAL.md`; don't re-grow them) | same change that alters a Makefile target or the build/test procedure |
| `MANUAL.md` | **end-user manual**: commands, configuration, behavior, known bugs & limitations | **any commit that changes user-visible behavior or bug status** — same commit, never after |

## Project state model

- The **phase system (0–7) is finished and retired** — those docs live in `docs/history/` and are context, not work. Do not resume phase checklists.
- Active work is organized as **workstreams W1–W9** in `docs/plan.md`. W1 (correctness fixes) blocks everything else.
- Decisions belong to **Max**; agents propose, `decisions.md` records who/when/why.

## Hard rules

- **Never `git push`.** Commit when asked or when a work item completes; Max pushes himself.
- `go build ./... && go vet ./... && go test ./...` green at every commit.
- Builds are **native per platform** (no cross-compile requirement); cgo is permitted where the OS's best API needs it (spec §10). Pure Go preferred where equal.
- Tests land with the feature, as `docs/testing.md` rows.
- **`MANUAL.md` stays true at every commit.** Any change to commands, flags, configuration, defaults, or user-visible behavior — and any change to known-bug status, a bug *found or fixed* — updates the manual in the same commit. A fixed bug leaves the Known-bugs list in that fix's commit.
- **Doc claims are claims about code, not intent.** Any user-facing statement in `MANUAL.md`/`README.md` that asserts *where* something surfaces (`status` vs `sync` output vs logs), *what* a command or flag does, or a *default value* must be traced to the code path that produces it — verify against the code, not the spec. The spec states intent; the code states reality, and they drift (e.g. skips are reported by `sync` but never by `status` — caught 2026-07-24, decisions.md). The CLI command/flag surface additionally has an automated golden guard: `cmd/synckeeper/surface_test.go` fails when a command or flag is added, renamed, or removed until its manifest — and the matching `MANUAL.md` §3 / spec §15 rows — are updated in the same commit.
- **Append to `docs/worklog.md` as you work.** Every user prompt gets an entry — the prompt quoted verbatim, a `YYYY-MM-DD HH:MM` timestamp, and one short sub-bullet per action or decision, precise enough to reconstruct what happened. Append at the bottom, never rewrite or reorder past entries, and commit it (its own `docs: worklog …` commit or folded into the work's commit). It supplements — never replaces — `status.md`, `decisions.md`, and `testing.md`.
- Durability invariants (spec §3) are non-negotiable; invariant 7 (dependency-aware plan ordering) exists because ordering bugs shipped once — see decisions.md 2026-07-17.
- The credential model (spec §9): Max's OAuth client credentials stay embedded as the default; never "fix" that by removing them.

## Commands

```sh
make test                              # full offline suite (in-memory Drive fake)
SYNCKEEPER_LIVE_TEST=1 go test ./...   # live smoke vs a throwaway Drive folder
SYNCKEEPER_SOAK_SECONDS=7200 go test ./internal/watch/ -run TestSoak -timeout 3h -v  # -v: pass criteria read the "converged on N files" line
SYNCKEEPER_SCALE_FILES=50000 go test ./internal/watch/ -run 'TestScale|TestFSEventsScale' -timeout 20m  # W1-scale acceptance
SYNCKEEPER_FUZZ_RUNS=60 SYNCKEEPER_FUZZ_STEPS=150 go test ./internal/engine -run TestFuzzConvergence  # deep fuzz (W4)
```
