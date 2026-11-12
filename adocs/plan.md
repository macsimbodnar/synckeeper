# Plan

Synckeeper is a personal bidirectional sync between one local folder and one Google Drive folder, in Go, daemon-first. The engine is written; what is left is the platform rollout (macOS and Linux are live, Windows is untouched), a small set of found defects, and the observability gaps the field keeps surfacing. Detail lives in [specs.md](specs.md); **correctness outranks features and ports**, and it has repeatedly jumped the queue on exactly that rule.

Order lives here and nowhere else. Step detail lives in the step files under
`plan_todo/`, `plan_current/`, and `plan_done/`. Ids are allocated in creation
order and never renumbered, so reordering is a one-line edit to this list.
Every open step file appears as an entry under Open, and every Open entry has
a step file — keep both true by hand. An id named in prose is prose: it does
not change the order.

Work finished before 2026-09-03 predates this plan structure: it is recorded as workstreams W1–W21 in [plan_history.md](plan_history.md), not as `plan_done/` step files. `plan_done/` starts empty and holds everything from S001 onward.

## Open

1. S036  Plans still refused every cycle in wider shapes — two folder levels, held rows, long chains (PRE-EXISTING; 1,014 of 60,000 generated inputs)
2. S020  A folder renamed on Drive beats its local deletion (DEC-006)
3. S017  The sync folder is identified by a hidden marker file — a different or unmounted folder is refused, not read as emptied (DEC-005)
4. S022  One failing action no longer switches off quarantine purge, deletion report and activity detail (DEC-008)
5. S032  How strong must rename evidence be — one identical file is enough today (DEC-012: most of the folder's non-empty files must be found unchanged)
6. S019  Remote-write gate on real Drive: live verification, narrower window (live runs authorized by Max 2026-11-11)
7. S015  `watch`/`sync` never check the Drive root (audit F10; live confirmation first — authorized)
8. S030  Case-insensitive folder: a Drive newcomer winning the fold wedges a download against the file on disk (found 2026-11-12) — rule decided, DEC-011: a reported skip, named as a name conflict
9. S029  The `internal/watch` tests reach the developer's real system bin (found 2026-11-12)
10. S028  Linux: `status`/`doctor`/`info` name the home bin, not the sync dir's (S016 residual)
11. S034  A delegated `sync` may print `Done.` for a refused cycle (from reading)
12. S003  Daemon surfaces skips in `activity`
13. S004  Stored error details keep the URL and lose the cause
14. S005  "N local changes" counts wake-ups, not files
15. S006  Third platform: Windows
16. S012  Add a LICENSE (audit F06) — distribution, deferred and uncertain

**Order is Max's, 2026-11-11 (DEC-002): audit bugs first, distribution last and possibly never.** S007–S016 come from the audit [audit/2026-11-11_adversarial.md](audit/2026-11-11_adversarial.md), one step per finding (F09 maps onto the existing S002). Fixes are implemented by Opus subagents, one per step, sequentially, coordinated by the main session. Max decided the six open shapes on 2026-11-11 (DEC-004..DEC-009): S002, S020, S017, S022 are runnable; **S024 and S025 are closed unbuilt, accepted by decision**. S019 and S015 run against real Drive with Max's permission. S016 and S001's L8 run on `pop-os`, in a session Max starts there.

**Step ids S027–S030 were allocated twice on 2026-11-12** (Mac and `pop-os`, both from `5c1a807`). At the merge the Mac side took the next free ids: S027 → S031 (moves stage), S028 → S032 (rename evidence), S029 → S033 (tui `-race` flake), S030 → S034 (delegated `sync`). S027–S030 are the `pop-os` steps. Read-only text written before the merge — `plan_done/S002`'s stamp, worklog entries of 02:12, commits `cdc893b` and `e27c8bc` — still uses the old Mac numbers.

Behind the audit steps the earlier order stands: S001 → S003 is Max's, recorded 2026-08-23 (W7 → W22 → W10; S002, then W22, now runs with the audit bugs). S004 and S005 were found 2026-09-03 and appended rather than inserted: both are cheap, self-contained, and independent, so they are candidates to jump — Max's call, a one-line edit here.

## Parked

Not steps, and not forgotten. Reopening either is a decision.

- **Tray / menu-bar app** (W9, parked 2026-07-27) — W15 delivered the TUI dashboard as the CLI's UI, so the tray became optional polish rather than capability. Reviving it stays cheap by design: W15-D5's `stat` control payload is specified as the shared client read model. File-manager badges are unaffected (no API on the primary platform).
- **Field incident: a Drive-bin delete came back as a re-upload** (W12, Linux, 2026-07-25) — parked the same day; the trigger was never identified. A month of two-machine use without recurrence (Max's field report, 2026-08-23) is the strongest evidence yet for leaving it parked. Reopen only if it recurs.

## Done recently

Last five; the full record is `plan_done/`.

- S035  A local rename or download whose commit is lost is recorded on recovery; a Drive folder arriving where a folder still sits here is merged; a deleted-and-remade folder no longer doubles on Drive — fixed 2026-11-12 (nine fuzz seeds, four causes; both 200-seed sweeps green again)
- S031  The moves stage: no local file replaced unless its bytes are kept (rule O1, checked on every plan), file moves in dependency order, swaps through a temporary name — fixed 2026-11-12 (pre-existing data loss; residuals S035, S036; DEC-010)
- S033  A flaky `-race` failure in `internal/tui` — closed 2026-11-12 as a duplicate of S027 (fixed on `pop-os`); 50 `-race` runs green on the Mac
- S001  Finish the Linux port — L8 hardware gates passed on `pop-os` (2 h soak converged on 16,419 files), L7 docs — done 2026-11-12
- S027  `go test -race` green on Linux: the dashboard's cancellation takes bubbletea's graceful path — fixed 2026-11-12 on `pop-os`

Earlier work: [plan_history.md](plan_history.md), W1–W21.
