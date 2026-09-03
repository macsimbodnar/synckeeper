# Agent operating rules

Applies to any agent working in this repository. `adocs/` is the project's
memory; these rules say how to read it and how to keep it current. Nothing
here is machine-enforced: the rules work because agents follow them, and a
violation is visible in the diff. Set up by `/moltke:init`, which also wrote
the `## Project rules` section at the end — the per-project answers that
complete this generic base. Change those with `/moltke:rules`.

Claude Code entry point: `CLAUDE.md` containing `@AGENTS.md`. Instructions
layer, most specific wins: `.moltke.local.md` (machine-local, uncommitted)
overrides `## Project rules`, which overrides the base ruleset above it.

## Orient

Start every session by reading, in this order:

1. `adocs/status.md` — last done, in progress, next, blocked, parked.
2. `adocs/plan.md` — what is being built, and the ordered open steps.
3. `.moltke.local.md`, if present — machine-local notes, uncommitted.

That is enough to act on. Go deeper only on demand:

- `adocs/specs.md` — what the project must do and never break. Read whole
  before changing behaviour; it holds current state only.
- `adocs/decisions.md` — why things are the way they are. Never read whole:
  grep the index by id or topic.
- `adocs/plan_done/` — one file per finished step. Read one when you need to
  know how something got the way it is.

Precedence when they disagree: **specs > plan > status**. Filesystem beats
prose: the plan directories are the state and `status.md` is a view of them —
regenerate the view, never bend the directories to match it. Code that
disagrees with specs is a bug or an unrecorded decision, never silently the
new truth.

## The plan

One file per step. The file's directory is its state, and state changes by
moving the file — by hand, deliberately:

| Directory | Meaning |
|---|---|
| `adocs/plan_todo/` | agreed, not started |
| `adocs/plan_current/` | in progress |
| `adocs/plan_done/` | finished, `done:` stamp written last |

Step files are named `S<nnn>_short_name.md`:

```
id:         S042
goal:       one line
accepts:    what proves it done, testable
touches:    areas affected
excludes:   explicitly out of scope
closes:     <!-- audit finding ids, when any -->
paused_by:  <!-- blocking child's id, only while paused -->
author:     <!-- who claimed it, set on start -->
done:       <!-- completion stamp: what proves it finished, written last -->
```

A new id is one more than the highest ever allocated, across all three
directories — ids are never reused or renumbered, even for a deleted step.
The one exception is a merge collision: two branches allocated the same id,
and the not-yet-merged side takes the next free id — file and `plan.md`
entry — before merging, noted in the merge commit; two files claiming one id
is worse.
Order lives in `plan.md`'s Open list and nowhere else; the next step is the
first entry there.

- **Start**: move todo → current, set `author:`. Respect PLAN's active limit;
  a paused step does not count against it.
- **Discovered mid-step**: trivial and in scope — fix it now, note it in the
  stamp. Blocking — create the blocker directly in `plan_current/`, set
  `paused_by:` on the parent. Independent — a new step in `plan_todo/`,
  however tempting.
- **Finish**: `accepts` holds and the TESTS and DOCS rules are satisfied; then
  write the `done:` stamp, move the file to `plan_done/`, update `plan.md`
  (out of Open, into the last-five Done list) and `status.md`, and commit per
  COMMITS. Completing a blocking child clears the parent's `paused_by:`.
- `plan_done/` is history: never edit, rewrite, or delete anything in it. A
  done step that got something wrong gets a new step or a decision, not an
  edit.
- When the plan meets reality and loses: stop, record a decision, amend the
  plan. Never deviate silently.

## Keep the memory current

- `status.md`: rewrite by hand at the end of any turn that changed plan
  state. Everything under `Parked:` is human memory — carry it forward, prune
  it only deliberately.
- `specs.md`: a behaviour change updates its wording in the same commit.
- `decisions.md`: a choice a future reader would re-derive gets a `DEC-<nnn>`
  entry when it is made, not after. Index on top, newest entry last, ids never
  reused. Format: heading, `Tags:`, `Decision:` (and by whom), `Why:` (one
  line). Decisions belong to the user; agents propose.
- Nothing that matters lives only in a transcript or an agent's memory. The
  repository is the memory.

## Review and audit

- **Fast check**, when REVIEW says so: after a step completes, one small
  subagent over that step's diff — top real problems only, one screen, no
  writes. Trivial → fix now. Real → a new step. Nothing → one line, move on.
- **Full audit**: `/moltke:audit` — an adversarial reviewer on a clean
  context writes a dated report under `adocs/audit/`; findings become steps
  or recorded decisions. Reports are evidence: never overwrite one, and the
  only edit an earlier report takes is a finding's `Status:` line moving
  (open → planned / closed / accepted).

## Project rules

Interview answers recorded by `/moltke:init`; change them with
`/moltke:rules` (or edit by hand), and record every change as a decision.
One line per rule, stable id first.

- GIT: commit freely; never push — the user pushes.
- AGENTS: subagents only for reviews and read-only exploration.
- TESTS: the suite is green before a step is marked done (`make test`); a defect gets a failing test before its fix.
- PLAN: one active (non-paused) step per agent.
- DOCS: README and MANUAL checked at every step completion.
- REVIEW: fast check after each completed step.
- AUDIT: full audits on demand only.
- DEPS: never add a dependency without asking; state what it buys and what it costs.
- COMMITS: commit at each completed step and at any plan change.
