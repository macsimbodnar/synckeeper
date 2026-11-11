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

1. S007  A symlinked `sync_dir` is scanned as empty — Drive folder trashed (audit F01, Critical)
2. S008  `init` with the sync folder gone and `state.db` kept empties Drive (audit F02, Critical)
3. S009  An interrupted mirror rebuild is read as mass deletion on Drive (audit F03, High)
4. S010  Remote-write gate — an upload overwrites a concurrent remote edit (audit F04, High)
5. S011  A file written during its upload lands truncated under its real name (audit F05, High)
6. S001  Finish the Linux port (remaining L-items, then the hardware gates)
7. S002  Fix the R25 crash-rename wedge — a refused plan repeats forever (audit F09)
8. S013  A tracked folder replaced by a symlink is trashed on Drive (audit F07, Medium)
9. S014  One unreadable file wedges every cycle (audit F08, Medium)
10. S015  `watch`/`sync` never check the Drive root (audit F10, Medium, live confirmation first)
11. S016  Linux mass-delete guard probes the wrong trash (audit F11, Medium, Linux hardware first)
12. S003  Daemon surfaces skips in `activity`
13. S004  Stored error details keep the URL and lose the cause
14. S005  "N local changes" counts wake-ups, not files
15. S012  Add a LICENSE (audit F06, publication blocker — license is Max's pick)
16. S006  Third platform: Windows

S007–S016 come from the audit [audit/2026-11-11_adversarial.md](audit/2026-11-11_adversarial.md), one step per finding (F09 maps onto the existing S002). **Their positions are agent-proposed, 2026-11-11, on "correctness outranks features and ports": Critical and High ahead of S001, Mediums behind S002 — Max reorders with a one-line edit.** F09 also reports that S002's recorded premise (reproduces only at `SYNCKEEPER_FUZZ_CRASH=1`) is false, which is what placed it behind S001; its position is left as Max set it.

The relative order S001 → S002 → S003 is Max's, recorded 2026-08-23 (W7 → W22 → W10). S004 and S005 were found 2026-09-03 and appended rather than inserted: both are cheap, self-contained, and independent of everything above them, so they are candidates to jump — that is Max's call, and a one-line edit here.

## Parked

Not steps, and not forgotten. Reopening either is a decision.

- **Tray / menu-bar app** (W9, parked 2026-07-27) — W15 delivered the TUI dashboard as the CLI's UI, so the tray became optional polish rather than capability. Reviving it stays cheap by design: W15-D5's `stat` control payload is specified as the shared client read model. File-manager badges are unaffected (no API on the primary platform).
- **Field incident: a Drive-bin delete came back as a re-upload** (W12, Linux, 2026-07-25) — parked the same day; the trigger was never identified. A month of two-machine use without recurrence (Max's field report, 2026-08-23) is the strongest evidence yet for leaving it parked. Reopen only if it recurs.

## Done recently

Last five; the full record is `plan_done/`.

- nothing yet under this structure — see [plan_history.md](plan_history.md) for W1–W21.
