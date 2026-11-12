@AGENTS.md

# Synckeeper — project notes

`AGENTS.md` is the ruleset: read order, the plan directories, how the memory is kept, and the recorded `## Project rules`. This file adds only what is specific to *this* codebase and is not in there.

The one rule that outranks everything: **the engine must never silently lose or corrupt a file.** It is the prime directive in [adocs/specs.md](adocs/specs.md), restated as INV-1..INV-9 at the top of that file.

## Files `AGENTS.md` does not name

The workflow's own files (`adocs/specs.md`, `plan.md`, `status.md`, `decisions.md`, the plan directories, `audit/`) are covered by the ruleset. These are this project's extras:

| File | Role | Update when |
|---|---|---|
| `adocs/testing.md` | acceptance ledger — rows are the criteria; what the TESTS rule points at | with the feature, never after |
| `adocs/worklog.md` | append-only action log, keyed by the prompt | every prompt — append the prompt verbatim + `YYYY-MM-DD HH:MM` + one short sub-bullet per action or decision. Append at the bottom; never rewrite or reorder past entries. Supplements, never replaces, `status.md`/`decisions.md`/`testing.md` |
| `adocs/ideas.md` | Max's informal wishlist / inbox | triage items into specs + plan |
| `adocs/plan_history.md` | retired workstreams W1–W21 (the pre-2026-09-03 structure) | never — read-only archive |
| `adocs/history/` | retired phase docs 0–7 (the pre-2026-07-17 phase system) | never — read-only archive |
| `README.md` | repo landing page (**developer-facing**): pitch, doc map, **code layout**, build/test instructions — **deliberately no usage sections** (those live in `MANUAL.md`; don't re-grow them) | same commit that alters a Makefile target, the build/test procedure, or the package layout |
| `MANUAL.md` | **end-user manual**: commands, configuration, behavior, known bugs & limitations | same commit that changes user-visible behavior or bug status — never after |

## Project state model

- The **phase system (0–7) is finished and retired**, and so are **workstreams W1–W21**. Both are context, not work: `adocs/history/` and `adocs/plan_history.md`. Do not resume either.
- Active work is steps `S001+` under the plan directories. **Correctness outranks features and ports** — W16, W17 and W18 each jumped the queue on it, and the principle stands.

## Hard rules beyond the ruleset

- `go build ./... && go vet ./... && go test ./...` green at every commit, not only at step completion.
- Builds are **native per platform** (no cross-compile requirement); cgo is permitted where the OS's best API needs it (specs §10). Pure Go preferred where equal.
- **The DOCS rule is a check, not a habit — `README.md` AND `MANUAL.md`, in the same commit.** **`MANUAL.md`** (end users): any change to commands, flags, configuration, defaults, user-visible behavior, or known-bug status (a bug *found or fixed* — a fixed bug leaves the Known-bugs list in that fix's commit). **`README.md`** (developers): any change to a Makefile target, the build/test/vet procedure, the package layout (`cmd/`, `internal/*`), or the developer workflow. Keep the two **split with minimal overlap** — usage lives only in `MANUAL.md`, code layout / build / test only in `README.md`. If a step genuinely touches neither, that's fine — but you must have checked.
- **Doc prose is minimalistic and information-dense.** Write `README.md`, `MANUAL.md`, and doc edits generally as terse as possible without losing a fact: fragments over sentences; drop articles, filler ("just", "simply", "basically"), hedging, and connective fluff; short synonyms; merge redundant bullets; keep one example where several show the same pattern; prefer scannable tables and short bullets so a reader finds the answer at a glance. **Preserve every fact — compression never drops information.** **Copy verbatim, never reword:** code blocks, inline code, commands, flags, file paths, config keys, URLs/links, versions, dates. When you touch a doc, match this density; if a doc has drifted verbose, tighten it.
- **Doc claims are claims about code, not intent.** Any user-facing statement in `MANUAL.md`/`README.md` that asserts *where* something surfaces (`status` vs `sync` output vs logs), *what* a command or flag does, or a *default value* must be traced to the code path that produces it — verify against the code, not the specs. The specs state intent; the code states reality, and they drift (e.g. skips are reported by `sync` but never by `status` — caught 2026-07-24, decisions.md). The CLI command/flag surface additionally has an automated golden guard: `cmd/synckeeper/surface_test.go` fails when a command or flag is added, renamed, or removed until its manifest — and the matching `MANUAL.md` §3 / specs §15 rows — are updated in the same commit.
- Durability invariants (specs §3) are non-negotiable; INV-7 (dependency-aware plan ordering) exists because ordering bugs shipped once — see decisions.md 2026-07-17.
- The credential model (specs §9): **no credentials are embedded** — the tool requires a user-supplied `credentials.json` (a Google Cloud "Desktop app" OAuth client). Never re-add a hardcoded client id/secret to the source or git history; the `internal/audit` gate (`make audit`) enforces this. *(Reversed 2026-07-25: the embedded default was removed and scrubbed from all history after GitHub push protection flagged it — see decisions.md. The prior rule said to keep it embedded; that is void.)*

## Commands

```sh
make test                              # full offline suite (in-memory Drive fake; includes the secret-scan gate)
make audit                             # pre-publication secret-scan gate on its own (internal/audit)
SYNCKEEPER_LIVE_TEST=1 go test ./...   # live smoke vs a throwaway Drive folder
SYNCKEEPER_SOAK_SECONDS=7200 go test ./internal/watch/ -run TestSoak -timeout 3h -v  # -v: pass criteria read the "converged on N files" line
SYNCKEEPER_SCALE_FILES=50000 go test ./internal/watch/ -run 'TestScale|TestFSEventsScale' -timeout 20m  # W1-scale acceptance
SYNCKEEPER_FUZZ_RUNS=60 SYNCKEEPER_FUZZ_STEPS=150 go test ./internal/engine -run TestFuzzConvergence  # deep fuzz (W4); SYNCKEEPER_FUZZ_CRASH is a PROBABILITY (default 0.12), not a flag — the recorded sweeps use the default
SYNCKEEPER_PLANFUZZ_RUNS=100000 go test ./internal/reconcile -run TestPlanFuzz  # deep random plan sweep (S031): planner inputs → both §4.5 validators + a model executor; default 5000 per shape runs in the plain suite
```
