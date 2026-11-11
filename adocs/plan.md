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

1. S002  Fix the R25 rename wedge — planner fixed for every route + a repeating refused plan visible in `status` (audit F09; DEC-004)
2. S020  A folder renamed on Drive beats its local deletion (DEC-006)
3. S017  The sync folder is identified by a hidden marker file — a different or unmounted folder is refused, not read as emptied (DEC-005)
4. S022  One failing action no longer switches off quarantine purge, deletion report and activity detail (DEC-008)
5. S019  Remote-write gate on real Drive: live verification, narrower window (live runs authorized by Max 2026-11-11)
6. S015  `watch`/`sync` never check the Drive root (audit F10; live confirmation first — authorized)
7. S027  `go test -race` red on Linux: the dashboard's exit races bubbletea's input reader (blocks S001's L8; found 2026-11-12)
8. S001  Finish the Linux port — L8 gates on `pop-os`, then L7 docs (paused by S027)
9. S029  The `internal/watch` tests reach the developer's real system bin (found 2026-11-12)
10. S028  Linux: `status`/`doctor`/`info` name the home bin, not the sync dir's (S016 residual)
11. S003  Daemon surfaces skips in `activity`
12. S004  Stored error details keep the URL and lose the cause
13. S005  "N local changes" counts wake-ups, not files
14. S006  Third platform: Windows
15. S012  Add a LICENSE (audit F06) — distribution, deferred and uncertain

**Order is Max's, 2026-11-11 (DEC-002): audit bugs first, distribution last and possibly never.** S007–S016 come from the audit [audit/2026-11-11_adversarial.md](audit/2026-11-11_adversarial.md), one step per finding (F09 maps onto the existing S002). Fixes are implemented by Opus subagents, one per step, sequentially, coordinated by the main session. Max decided the six open shapes on 2026-11-11 (DEC-004..DEC-009): S002, S020, S017, S022 are runnable; **S024 and S025 are closed unbuilt, accepted by decision**. S019 and S015 run against real Drive with Max's permission. S016 and S001's L8 run on `pop-os`, in a session Max starts there.

Behind the audit steps the earlier order stands: S001 → S003 is Max's, recorded 2026-08-23 (W7 → W22 → W10; S002, then W22, now runs with the audit bugs). S004 and S005 were found 2026-09-03 and appended rather than inserted: both are cheap, self-contained, and independent, so they are candidates to jump — Max's call, a one-line edit here.

## Parked

Not steps, and not forgotten. Reopening either is a decision.

- **Tray / menu-bar app** (W9, parked 2026-07-27) — W15 delivered the TUI dashboard as the CLI's UI, so the tray became optional polish rather than capability. Reviving it stays cheap by design: W15-D5's `stat` control payload is specified as the shared client read model. File-manager badges are unaffected (no API on the primary platform).
- **Field incident: a Drive-bin delete came back as a re-upload** (W12, Linux, 2026-07-25) — parked the same day; the trigger was never identified. A month of two-machine use without recurrence (Max's field report, 2026-08-23) is the strongest evidence yet for leaving it parked. Reopen only if it recurs.

## Done recently

Last five; the full record is `plan_done/`.

- S016  Linux: the mass-delete guard probes the trash the sync dir really uses — fixed 2026-11-12 on `pop-os` (audit F11)
- S025  Held paths keep no memory — NOT BUILT, accepted (DEC-009)
- S024  Partial-upload crash window — NOT BUILT, accepted (DEC-007)
- S023  A file renamed the same way on both sides and edited on one is an update, not a conflict — fixed 2026-11-11 (DEC-003)
- S026  `doctor` lists held paths as held, not missing — fixed 2026-11-11

Earlier work: [plan_history.md](plan_history.md), W1–W21.
