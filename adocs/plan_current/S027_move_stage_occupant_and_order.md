id:         S027
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
            be (S028). A folder rename differing only in case (Parked directory arm).
closes:
paused_by:
author:     Opus subagent, coordinated by Claude (agent); started 2026-11-12 at Max's direction (DEC-002)
done:
