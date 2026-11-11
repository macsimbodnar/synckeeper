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

1. S014  One unreadable file wedges every cycle (audit F08, Medium)
2. S018  A moved-in Drive folder whose walk fails once is never walked again — contents never download (S009 fast check, reproduced)
3. S021  A folder trash does not look inside the folder — a file edited or added there meanwhile is binned (left open by S010; reproduced on the fake)
4. S023  Both sides rename a file to the same name + a local edit → conflict instead of upload (S011 fast check, reproduced; same code path as S002)
5. S017  Baseline is not bound to the sync folder — a retargeted link or edited `sync_dir` reads as deletion (S007 fast check; design decided with Max first)
6. S020  A folder deleted here while renamed there: delete wins badly (S010 fast check, reproduced; rule decided with Max first)
7. S024  Crash between a partial upload landing and its commit — the window S011 left open (audit F05; mechanism decided with Max first)
8. S022  One action failing every cycle keeps quarantine purge, deletion warning and activity detail off (S011 fast check; outcome wording confirmed with Max)
9. S025  Folder swapped for a symlink: the routes S013 left open (link renamed, parent renamed, swap under a running daemon; Max decides whether and how)
10. S002  Fix the R25 crash-rename wedge — a refused plan repeats forever (audit F09; shape decided with Max first)
11. S019  Remote-write gate on real Drive: live verification, narrower window (S010 fast check; live Drive and Max's pick first)
12. S015  `watch`/`sync` never check the Drive root (audit F10, Medium, live confirmation first)
13. S016  Linux mass-delete guard probes the wrong trash (audit F11, Medium, Linux hardware first)
14. S001  Finish the Linux port (remaining L-items, then the hardware gates)
15. S003  Daemon surfaces skips in `activity`
16. S004  Stored error details keep the URL and lose the cause
17. S005  "N local changes" counts wake-ups, not files
18. S006  Third platform: Windows
19. S012  Add a LICENSE (audit F06) — distribution, deferred and uncertain

**Order is Max's, 2026-11-11 (DEC-002): audit bugs first, distribution last and possibly never.** S007–S016 come from the audit [audit/2026-11-11_adversarial.md](audit/2026-11-11_adversarial.md), one step per finding (F09 maps onto the existing S002). Fixes are implemented by Opus subagents, one per step, sequentially, coordinated by the main session. Nine steps cannot simply be run: S017, S020, S024, S022, S025 and S002 need their shape decided or confirmed with Max, S019 needs live Drive and a pick, S015 needs a live-Drive measurement, S016 needs Linux hardware.

Behind the audit steps the earlier order stands: S001 → S003 is Max's, recorded 2026-08-23 (W7 → W22 → W10; S002, then W22, now runs with the audit bugs). S004 and S005 were found 2026-09-03 and appended rather than inserted: both are cheap, self-contained, and independent, so they are candidates to jump — Max's call, a one-line edit here.

## Parked

Not steps, and not forgotten. Reopening either is a decision.

- **Tray / menu-bar app** (W9, parked 2026-07-27) — W15 delivered the TUI dashboard as the CLI's UI, so the tray became optional polish rather than capability. Reviving it stays cheap by design: W15-D5's `stat` control payload is specified as the shared client read model. File-manager badges are unaffected (no API on the primary platform).
- **Field incident: a Drive-bin delete came back as a re-upload** (W12, Linux, 2026-07-25) — parked the same day; the trigger was never identified. A month of two-machine use without recurrence (Max's field report, 2026-08-23) is the strongest evidence yet for leaving it parked. Reopen only if it recurs.

## Done recently

Last five; the full record is `plan_done/`.

- S013  A tracked path replaced by a symlink is held, not read as deleted — fixed 2026-11-11 (audit F07; residual routes S025)
- S011  A file written during its upload — partial upload recorded as our own, re-uploaded next cycle — fixed 2026-11-11 (audit F05; crash window open, S024)
- S010  Remote-write gate — a concurrent Drive write refuses the mutation — fixed 2026-11-11 (audit F04; residuals S019, S021)
- S009  An interrupted mirror rebuild is read as mass deletion on Drive — fixed 2026-11-11 (audit F03)
- S008  `init` with the sync folder gone and `state.db` kept — fixed 2026-11-11 (audit F02)

Earlier work: [plan_history.md](plan_history.md), W1–W21.
