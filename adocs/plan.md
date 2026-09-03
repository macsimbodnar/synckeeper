# Plan

Synckeeper is a personal bidirectional sync between one local folder and one Google Drive folder, in Go, daemon-first. The engine is written; what is left is the platform rollout (macOS is live, Linux is in flight, Windows is untouched), a small set of found defects, and the observability gaps the field keeps surfacing. Detail lives in [specs.md](specs.md); **correctness outranks features and ports**, and it has repeatedly jumped the queue on exactly that rule.

Order lives here and nowhere else. Step detail lives in the step files under
`plan_todo/`, `plan_current/`, and `plan_done/`. Ids are allocated in creation
order and never renumbered, so reordering is a one-line edit to this list.
Every open step file appears as an entry under Open, and every Open entry has
a step file — keep both true by hand. An id named in prose is prose: it does
not change the order.

Work finished before 2026-09-03 predates this plan structure: it is recorded as workstreams W1–W21 in [plan_history.md](plan_history.md), not as `plan_done/` step files. `plan_done/` starts empty and holds everything from S001 onward.

## Open

1. S001  Finish the Linux port (remaining L-items, then the hardware gates)
2. S002  Fix the R25 crash-rename wedge — a refused plan repeats forever
3. S003  Daemon surfaces skips in `activity`
4. S004  Stored error details keep the URL and lose the cause
5. S005  "N local changes" counts wake-ups, not files
6. S006  Third platform: Windows

Order 1–3 is Max's, recorded 2026-08-23 (W7 → W22 → W10) and unchanged by this migration. S004 and S005 were found 2026-09-03 and appended rather than inserted: both are cheap, self-contained, and independent of everything above them, so they are candidates to jump — that is Max's call, and a one-line edit here.

## Parked

Not steps, and not forgotten. Reopening either is a decision.

- **Tray / menu-bar app** (W9, parked 2026-07-27) — W15 delivered the TUI dashboard as the CLI's UI, so the tray became optional polish rather than capability. Reviving it stays cheap by design: W15-D5's `stat` control payload is specified as the shared client read model. File-manager badges are unaffected (no API on the primary platform).
- **Field incident: a Drive-bin delete came back as a re-upload** (W12, Linux, 2026-07-25) — parked the same day; the trigger was never identified. A month of two-machine use without recurrence (Max's field report, 2026-08-23) is the strongest evidence yet for leaving it parked. Reopen only if it recurs.

## Done recently

Last five; the full record is `plan_done/`.

- nothing yet under this structure — see [plan_history.md](plan_history.md) for W1–W21.
