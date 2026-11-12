id:         S020
goal:       A folder deleted on one machine while another renames it on Drive ends with
            the delete winning badly. Reproduced by S010's fast check (2026-11-11), on
            `06df839` and with S010 alike: `D/x.txt`, `D/y.txt` synced on A and B; A
            deletes `D`; `D` is renamed to `E` on Drive. A's next plan is `[move_local
            D->E, trash_remote D/y.txt, trash_remote D/x.txt, trash_remote E]`: the
            `move_local` fails (no such directory), the three trashes pass — as three
            separate Drive-bin entries, so restoring `E` does not bring its files back.
            B then quarantines `E` into its system bin. The plan contradicts itself,
            the rename is discarded, A reports `Failed=1` and `DeletedRemote=0`
            (`reportDeletions` skips a cycle with a failure, `engine.go:296`), and
            nobody is told a rename lost to a delete. Spec §4.2's "deleted | changed or
            moved → download" is per file; the planner does not count a parent rename
            as a file move, so the letter is arguable.
accepts:    Max picks the rule first: a remote folder rename beats a local folder
            delete (download `E` back, as §4.2 does per file), or the delete wins —
            then cleanly: no failing `move_local`, one collapsed Drive-bin entry, the
            deletion reported. A red-first engine test with the exact shape above for
            the chosen rule; specs §4.2/§4.4 state it; MANUAL §8 row leaves the list;
            testing.md rows.
touches:    internal/reconcile, internal/engine, MANUAL.md, adocs/specs.md,
            adocs/testing.md
excludes:   Rule decided by Max 2026-11-11 (DEC-006): the remote rename beats the local
            delete — the deleting machine gets the folder back under its new name,
            nothing is trashed on Drive.
closes:
paused_by:
author:     Opus subagent, coordinated by Claude (agent); started 2026-11-12 (DEC-002)
done:       2026-11-12 — two rounds by one Opus subagent, an independent check after
            each (the second with no new finding), coordinated and committed by Claude
            (agent). DEC-006 built: a folder gone here (from every place the disk could
            hold it) that Drive renamed or moved is dropped from the folder moves and
            comes back as a `MkdirLocal` at Drive's path carrying its id; its files meet
            §4.2's "deleted | moved → download". Nothing trashed, no deletion counted,
            zero failed actions, one cycle. The step's shape before: `move_local D→E`
            (fails), three `trash_remote`.
            Neighbours, by existing rules (specs §4.2): a file or subfolder deleted
            here inside a folder Drive renamed stays deleted (a subfolder as ONE
            Drive-bin entry — `trash_remote` now names the path Drive holds); Drive's
            rename plus edits/additions/deletions → restored with them; a done move
            without evidence downloads D's missing files into the folder; a file at the
            folder's OLD path is a new file (round 2; at HEAD the file was moved as if
            it were the folder and D's content binned); a file at the NEW path → type
            clash, reported; a second live Drive folder of that name → the restore
            waits, reported; a swap → `MkdirLocal` after the move that frees the path.
            Round 2 came from the first check's five findings (testing.md): the worst,
            on a case-folding disk, sent D's files to three bins.
            Coordinator's calls, DEC-013: the S002.17 test re-targeted under DEC-006;
            a fold-equal local folder without evidence is a reported name clash; a
            folder renamed differently on two machines keeps both (as files do).
            Tests: testing.md S020.1 onward, red first; `TestPlanFuzz`'s folder and wide
            shapes delete the local folder in a share of inputs.
            Verification (coordinator, final tree): build, vet, gofmt, Linux build+vet,
            `make test`, `-race` ×7, deep plan fuzz, 60×150, crash=1 ×40, crash=1 ×200
            green. **200×150 red on seed 102** — pre-existing case-fold defect of two
            files, repro red on `5a935a5` → S030 (first in Open).
            Not closed: S038 (case-fold FOLDERS with evidence: Drive gets `E` and `e`,
            a new file can end only in a system bin — pre-existing); S039 (the plain
            receiver loops on a Drive-side folder swap / duplicate folder names —
            pre-existing); S037's one-time failures on the other machine. Never run:
            real Drive, Linux runtime, the case-fold tests on a case-sensitive disk
            (they skip there).
            Docs: MANUAL §6/§8, specs §4.2/§4.4, testing.md.
