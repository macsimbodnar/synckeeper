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
done:
