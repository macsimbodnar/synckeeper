id:         S038
goal:       On a case-insensitive disk (macOS default) a FOLDER name differing only in
            case from Drive's is not handled: the engine creates the case-sibling on
            Drive and the machines then work against each other. PRE-EXISTING (same at
            `5a935a5`); this is status.md's parked "directory arm" (case-only dir
            renames, dir fold-adopts — decisions.md "W1.9.1"/"W1.8.8"), now with a
            reproduced harm. Reproduced by S020's second independent check
            (2026-11-12), two machines on a folding disk:
            A and B synced `D/x.txt`, `D/y.txt`. A renames `D` → `e`; Drive (B) renames
            `D` → `E`. A's plan: `move_local D->E, mkdir_remote e, move_remote
            D/x.txt->e/x.txt, move_remote D/y.txt->e/y.txt, trash_remote E` — Drive
            holds `E` and `e`. Settles in two rounds only if nothing else happens.
            If B meanwhile:
            - edits `E/x.txt` → A fails `download E/x.txt` every cycle and never gets
              the edit; B's `y.txt` goes to B's system bin (Drive keeps it under `e/`,
              B never gets it back); never settles;
            - adds `E/z.txt` → settles after 7 rounds of failures; **`z.txt` ends only
              in B's system bin, not on Drive**; Drive's bin +4;
            - adds `E/S/s.txt` → never settles; B's system bin collects duplicates.
            Also in the family: a Drive case-only folder rename `D` → `d` retries
            `move_local` every cycle (S020's implementer, at HEAD); MANUAL §8's "two
            machines with folder names differing only in case or accents can mint a
            duplicate folder in Drive".
            S020 built the hold for the case WITHOUT evidence (a fold-equal local
            folder that is not provably the same one → name clash, reported, left
            alone); with evidence the plan is still the one above.
accepts:    Red-first, engine level, two machines on a folding disk, for the three
            variants above and the case-only folder rename in both directions: no
            action creates on Drive a sibling folder differing only by case (or
            normalization) from one already there or created by the same plan; no file
            leaves the tree except by a deletion the user made; converges with zero
            failed actions, or holds with a reported, typed name-conflict skip
            (DEC-011's form) where the disk cannot hold both. The executor's
            case-only directory rename if the chosen rule needs it. `TestPlanFuzz`
            gains a case-fold variant if affordable (S020's estimate: 80–120 lines in
            the model executor). MANUAL §8/§9; specs §5; testing.md.
touches:    internal/reconcile, internal/executor (local write gate, directory arm),
            internal/remotedelta, tests, MANUAL.md, adocs/specs.md, adocs/testing.md
excludes:   S030 (files: a fold winner that is not the file on disk) — same family,
            do S030 first; S032.
closes:
paused_by:
author:
done:
