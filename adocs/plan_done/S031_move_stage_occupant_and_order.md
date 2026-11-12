id:         S031
goal:       The moves stage can destroy an unsynced local edit. (F4, PRE-EXISTING —
            the same plan on `5f61255`, reproduced at plan level by S002's third fast
            check, 2026-11-12.) File moves are sorted by depth, then path
            (`internal/reconcile/plan.go`, the moves-stage sort), not by dependency,
            and the move-destination occupant check treats a tracked occupant whose
            Drive item is alive as "bytes safe on Drive" even when it was edited
            locally — it pins the edited file's scanned stat and the executor renames
            over it. Flat input: Base `f.txt` (F,X), `a.txt` (G,MG); Local `f.txt`
            EDITED (Q), `a.txt`; Drive `h.txt` (F,X), `f.txt` (G,MG) — another machine
            renamed f→h and moved a onto f. Plan: `move_local a.txt→f.txt` (clobbers
            Q, no copy), `move_local f.txt→h.txt` (moves a's bytes), `record f.txt G`,
            `update_remote h.txt F` (uploads a's bytes into F). Q is gone and F holds
            the wrong content. It only works today when the vacating file's path
            happens to sort first. INV-7 exists because ordering bugs shipped before.
            Same root, left open by S002: (F1) under a folder move the disk already
            shows, the forget arm emits no move for the row, so another row's move
            clobbers the edit at `N/f.txt` (Base `D`, `D/f.txt` (F,X), `g.txt` (G,MG);
            Local `N/`, `N/f.txt` (Q), `g.txt`; Drive `N` (FD), `N/h.txt` (F,X),
            `N/f.txt` (G,MG)); (F2) the same input plus a local `N/h.txt` (X): the path
            released to pass 2 is still another row's move target → "record and upload
            both act on N/f.txt", refused every cycle; (F3, read) `Forget` is not a
            protector: if its DB write fails and the run continues, the folder's
            `move_local` commit re-homes the row and the next cycle uploads the
            unrelated file over Drive's (`internal/executor/executor.go`, `noteFailure`).
            Also in the family: a swap made by id on Drive fails its two move commits
            (`UNIQUE constraint failed: items.rel_path`) for a cycle or two before
            converging.
accepts:    Red-first, reconcile level and engine level (two machines), for F4 flat and
            one directory deeper, F1, F2: the local edit Q survives on every machine —
            at the renamed path if the baseline says only this side edited (§4.2), or
            as a conflict copy — nothing is uploaded into the wrong Drive item, no plan
            is refused. A tracked occupant whose local bytes differ from its baseline is
            never renamed over. File moves that depend on each other (one's destination
            is another's source; cycles = swaps) execute in dependency order, a swap
            through a temporary name or the existing mechanism, with zero failed
            actions. F3: a failed `Forget` blocks the folder commit that depends on it.
            The whole thing stays crash-safe: a crash between any two moves replans
            safely (crash checkpoints). All five fuzz sweeps stay green (default,
            60×150, crash=1 ×40, 200×150, crash=1 ×200). MANUAL §8's three rows leave in
            the fix's commit; specs §4.5; testing.md rows.
touches:    internal/reconcile (moves-stage ordering, occupant check, the forget arm),
            internal/executor (protectors), MANUAL.md, adocs/specs.md, adocs/testing.md
excludes:   S020 (folder deleted here vs renamed there). How strong rename evidence must
            be (S032). A folder rename differing only in case (Parked directory arm).
closes:
paused_by:
author:     Opus subagent, coordinated by Claude (agent); started 2026-11-12 at Max's direction (DEC-002)
done:       2026-11-12 — two rounds by one Opus subagent, an independent check after
            each, coordinated and committed by Claude (agent). Allocated as S027 on the
            Mac; renumbered S031 at the merge with the `pop-os` session (`a76875c`).
            Built: **rule O1** (`reconcile.ValidateLocalOverwrites`, specs §4.5) — the
            plan is simulated on the scan, and an action may replace a local file only
            if its bytes are kept (a Drive item's, a baseline row's synced content, or a
            download of this plan); a violation refuses the whole plan before anything
            runs and counts toward `stuck`; checked in `runPlan`, by the engine before
            `Apply`, and in the fuzz oracle. **Occupants** resolved after pass 1: no
            move is pinned to an occupant; it leaves first (own move, bin, conflict
            copy) or the incoming row waits (held row → reported skip); pinned
            downloads only over the row's own synced, kept bytes. **Order**: the
            file-moves stage sorted topologically (`moveorder.go`); cycles through a
            temporary conflict-copy name; a commit's destination freed in the baseline
            first; `Forget` is a protector, forgets chained (F3). Round 2 (regression
            found by the check — rename on both sides + new file at the old name on
            both, refused every cycle): a released file is new to pass 2, the
            created-on-both-sides rule applies; a second pre-existing class fixed on
            the way (moved-row download onto an untracked file → overlap refusal).
            Accepts: F4 flat and deeper, F1, F2, F3, chain, swap, rotation, every
            interruption of the swap and the chain — red first, now passing, reconcile
            and engine (testing.md S031.1–S031.22). `TestPlanFuzz` is new and
            permanent (random inputs → both validators + a model executor; deep run
            100k per shape: zero refusals, zero losses).
            DEVIATION from `accepts`, DEC-010: "all five sweeps stay green" holds with
            the checkpoint list the step started with (re-run on the final tree). The
            step added `move_before_commit` and `forget_before_commit`; with them
            200×150 is red on seeds 66, 91, 130 and crash=1 ×200 on 52, 56, 92, 122,
            128, 162 — nine pre-existing failures (same on `e27c8bc` + the two
            checkpoints), → S035. `TestS002SwapUnderADoneFolderMoveMakesNoCopy`
            re-targeted with Max's consent (DEC-010).
            Not closed: S035 (lost move commit); S036 (overlap refusals in wider
            shapes, 1,014 of 60,000, pre-existing); a Drive-side window from reading
            (two items under one name after a death between a remote move and the
            trash of the occupant — MANUAL §8). Rule O1 cannot see identity (right
            bytes, wrong Drive item) — that rests on ordering and `ProtectedBy`.
            Never run: real Drive, Linux runtime (Linux build+vet only).
            Docs: MANUAL §6/§7/§8, specs §4.5/§4.6/§8.2/§16, README and CLAUDE.md
            (`SYNCKEEPER_PLANFUZZ_RUNS`).
