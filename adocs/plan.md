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

1. S017  Baseline is not bound to the sync folder — a retargeted link or edited `sync_dir` reads as deletion (S007 fast check; design decided with Max first)
2. S020  A folder deleted here while renamed there: delete wins badly (S010 fast check, reproduced; rule decided with Max first)
3. S024  Crash between a partial upload landing and its commit — the window S011 left open (audit F05; mechanism decided with Max first)
4. S022  One action failing every cycle keeps quarantine purge, deletion warning and activity detail off (S011 fast check; outcome wording confirmed with Max)
5. S025  Held paths have no memory: routes S013/S014 left open (link or unreadable item renamed, swap under a running daemon; Max decides whether and how)
6. S002  Fix the R25 rename wedge — a refused plan repeats forever (audit F09; reachable WITHOUT a crash, found 2026-11-11; shape decided with Max first)
7. S019  Remote-write gate on real Drive: live verification, narrower window (S010 fast check; live Drive and Max's pick first)
8. S015  `watch`/`sync` never check the Drive root (audit F10, Medium, live confirmation first)
9. S016  Linux mass-delete guard probes the wrong trash (audit F11, Medium, Linux hardware first)
10. S001  Finish the Linux port (remaining L-items, then the hardware gates)
11. S003  Daemon surfaces skips in `activity`
12. S004  Stored error details keep the URL and lose the cause
13. S005  "N local changes" counts wake-ups, not files
14. S006  Third platform: Windows
15. S012  Add a LICENSE (audit F06) — distribution, deferred and uncertain

**Order is Max's, 2026-11-11 (DEC-002): audit bugs first, distribution last and possibly never.** S007–S016 come from the audit [audit/2026-11-11_adversarial.md](audit/2026-11-11_adversarial.md), one step per finding (F09 maps onto the existing S002). Fixes are implemented by Opus subagents, one per step, sequentially, coordinated by the main session. **Every step an agent can run alone is done (2026-11-11).** What is left at the top waits on Max: S017, S020, S024, S022, S025 and S002 need a shape, rule or confirmation; S019 and S015 need live Drive; S016 needs Linux hardware.

Behind the audit steps the earlier order stands: S001 → S003 is Max's, recorded 2026-08-23 (W7 → W22 → W10; S002, then W22, now runs with the audit bugs). S004 and S005 were found 2026-09-03 and appended rather than inserted: both are cheap, self-contained, and independent, so they are candidates to jump — Max's call, a one-line edit here.

## Parked

Not steps, and not forgotten. Reopening either is a decision.

- **Tray / menu-bar app** (W9, parked 2026-07-27) — W15 delivered the TUI dashboard as the CLI's UI, so the tray became optional polish rather than capability. Reviving it stays cheap by design: W15-D5's `stat` control payload is specified as the shared client read model. File-manager badges are unaffected (no API on the primary platform).
- **Field incident: a Drive-bin delete came back as a re-upload** (W12, Linux, 2026-07-25) — parked the same day; the trigger was never identified. A month of two-machine use without recurrence (Max's field report, 2026-08-23) is the strongest evidence yet for leaving it parked. Reopen only if it recurs.

## Done recently

Last five; the full record is `plan_done/`.

- S023  A file renamed the same way on both sides and edited on one is an update, not a conflict — fixed 2026-11-11 (DEC-003)
- S026  `doctor` lists held paths as held, not missing — fixed 2026-11-11
- S021  A folder trash checks the folder's contents on Drive first — fixed 2026-11-11 (left open by S010; live verification S019)
- S018  A folder entering the Drive tree is linked only after its walk completes — fixed 2026-11-11
- S014  One unreadable file is a held skip, not a wedge — fixed 2026-11-11 (audit F08; residual routes S025)

Earlier work: [plan_history.md](plan_history.md), W1–W21.
