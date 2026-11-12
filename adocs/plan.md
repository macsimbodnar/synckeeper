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

1. S030  Case-insensitive folder: an action aimed at a path held by a different local FILE under the same fold key — Drive newcomer winning the fold; a move arriving onto a new local file's case-twin (makes fuzz 200×150 red, seed 102). Rule: DEC-011, a reported skip named as a name conflict
2. S038  Case-insensitive folder: FOLDER names differing only in case mint a sibling on Drive; a file added elsewhere can end only in a system bin (PRE-EXISTING, reproduced 2026-11-12; the parked directory arm)
3. S017  The sync folder is identified by a hidden marker file — a different or unmounted folder is refused, not read as emptied (DEC-005)
4. S022  One failing action no longer switches off quarantine purge, deletion report and activity detail (DEC-008)
5. S032  How strong must rename evidence be — one identical file is enough today (DEC-012: most of the folder's non-empty files must be found unchanged)
6. S037  Actions planned at a path the same plan moves away fail once, then the next cycle is right (four folder-rename shapes; nothing lost)
7. S039  A machine that changed nothing loops on a Drive-side folder swap or on two Drive folders of one name (PRE-EXISTING; nothing lost)
8. S019  Remote-write gate on real Drive: live verification, narrower window (live runs authorized by Max 2026-11-11)
9. S015  `watch`/`sync` never check the Drive root (audit F10; live confirmation first — authorized)
10. S029  The `internal/watch` tests reach the developer's real system bin (found 2026-11-12)
11. S028  Linux: `status`/`doctor`/`info` name the home bin, not the sync dir's (S016 residual)
12. S034  A delegated `sync` may print `Done.` for a refused cycle (from reading)
13. S003  Daemon surfaces skips in `activity`
14. S004  Stored error details keep the URL and lose the cause
15. S005  "N local changes" counts wake-ups, not files
16. S006  Third platform: Windows
17. S012  Add a LICENSE (audit F06) — distribution, deferred and uncertain

**Order is Max's, 2026-11-11 (DEC-002): audit bugs first, distribution last and possibly never.** S007–S016 come from the audit [audit/2026-11-11_adversarial.md](audit/2026-11-11_adversarial.md), one step per finding (F09 maps onto the existing S002). Fixes are implemented by Opus subagents, one per step, sequentially, coordinated by the main session. Max decided the six open shapes on 2026-11-11 (DEC-004..DEC-009): S002, S020, S017, S022 are runnable; **S024 and S025 are closed unbuilt, accepted by decision**. S019 and S015 run against real Drive with Max's permission. S016 and S001's L8 run on `pop-os`, in a session Max starts there.

**Step ids S027–S030 were allocated twice on 2026-11-12** (Mac and `pop-os`, both from `5c1a807`). At the merge the Mac side took the next free ids: S027 → S031 (moves stage), S028 → S032 (rename evidence), S029 → S033 (tui `-race` flake), S030 → S034 (delegated `sync`). S027–S030 are the `pop-os` steps. Read-only text written before the merge — `plan_done/S002`'s stamp, worklog entries of 02:12, commits `cdc893b` and `e27c8bc` — still uses the old Mac numbers.

Behind the audit steps the earlier order stands: S001 → S003 is Max's, recorded 2026-08-23 (W7 → W22 → W10; S002, then W22, now runs with the audit bugs). S004 and S005 were found 2026-09-03 and appended rather than inserted: both are cheap, self-contained, and independent, so they are candidates to jump — Max's call, a one-line edit here.

## Parked

Not steps, and not forgotten. Reopening either is a decision.

- **Tray / menu-bar app** (W9, parked 2026-07-27) — W15 delivered the TUI dashboard as the CLI's UI, so the tray became optional polish rather than capability. Reviving it stays cheap by design: W15-D5's `stat` control payload is specified as the shared client read model. File-manager badges are unaffected (no API on the primary platform).
- **Field incident: a Drive-bin delete came back as a re-upload** (W12, Linux, 2026-07-25) — parked the same day; the trigger was never identified. A month of two-machine use without recurrence (Max's field report, 2026-08-23) is the strongest evidence yet for leaving it parked. Reopen only if it recurs.

## Done recently

Last five; the full record is `plan_done/`.

- S020  A folder renamed or moved on Drive beats its deletion here: it comes back under the new name, nothing trashed — built 2026-11-12 (DEC-006; DEC-013; residuals S030, S038, S039)
- S036  A subfolder renamed here inside a folder renamed on Drive is recognized as the same rename — the one cause behind 1,014 of 60,000 refused plans; the wide generator is a permanent shape of `TestPlanFuzz` — fixed 2026-11-12 (residual S037)
- S035  A local rename or download whose commit is lost is recorded on recovery; a Drive folder arriving where a folder still sits here is merged; a deleted-and-remade folder no longer doubles on Drive — fixed 2026-11-12 (nine fuzz seeds, four causes; both 200-seed sweeps green again)
- S031  The moves stage: no local file replaced unless its bytes are kept (rule O1, checked on every plan), file moves in dependency order, swaps through a temporary name — fixed 2026-11-12 (pre-existing data loss; residuals S035, S036; DEC-010)
- S033  A flaky `-race` failure in `internal/tui` — closed 2026-11-12 as a duplicate of S027 (fixed on `pop-os`); 50 `-race` runs green on the Mac

Earlier work: [plan_history.md](plan_history.md), W1–W21.
