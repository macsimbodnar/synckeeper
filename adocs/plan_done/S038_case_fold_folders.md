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
excludes:   S030 (files) — done 2026-11-12: its typed name-conflict skip, `ownCase` and
            pinned case-only `MoveLocal` for FILES are the model to extend; S032.
closes:
paused_by:
author:     Opus subagent, coordinated by Claude (agent); started 2026-11-12 (DEC-002)
done:       2026-11-12 — five rounds by one Opus subagent, four independent checks (a
            fresh reviewer each), coordinated and committed by Claude (agent). DEC-014.
            Built, folding disks only (no `CaseFold` false plan differs from `14ed882`:
            90,000 + 20,000 random inputs compared byte for byte, 19 pinned):
            a folder here at a folder's own path under the fold is that folder — the
            scan is re-keyed to the rows' spelling (`reconcile/casefold.go`), every
            action uses the row's spelling, and a case-only rename made here goes up
            as one `move_remote` appended last (ids kept); made on Drive → one
            `move_local` (the executor renames a directory onto a fold-equal name, in
            two steps where the OS makes it a no-op; recovery judges "gone" by exact
            spelling and finishes a cut rename). Renamed in case differently on two
            machines, or in case here and to another name on Drive → Drive's name.
            New folder here + new fold-equal folder on Drive → one folder, Drive's id
            and spelling. Two different fold-equal entries (folder/folder, folder/file,
            a Drive rename onto a name another local folder holds, a case rename onto
            a shadowed sibling's name) → S030's typed `name_conflict` hold, checked at
            every level of the path, released by renaming either named entry.
            The step's three variants (b edits / adds a file / adds a subfolder under
            `E` while a has `e`): one `move_local`, all trees `E/…`, D's id kept, zero
            failed, no bin entry, no fold pair on Drive.
            Fixed on the way, red at HEAD too: a move's commit stamped Drive's new md5
            on the row, so a download that then failed was never planned again (stale
            bytes, no error; `statedb.UpdateItemDriveVersion`); a file under an
            unreadable subfolder of a case-renamed folder went to Drive's bin;
            `statedb.RenameItemPath` matched descendants case-insensitively (rows at
            paths that exist nowhere; a later deletion came back).
            Three wrong plans came from a crash leaving rows in two spellings of one
            folder (lost `move_local`, lost download, re-home vs a Drive-side file
            case rename); the fuzz crash sweeps saw none → `TestS038CrashMatrix`
            (thinned in the plain suite, full behind `SYNCKEEPER_CRASH_MATRIX=1`:
            8,712 cases, 678 reached, 10 skipped as S037). Process death not covered.
            Nets: engine fuzzer gained `opCaseRename` (folding disks; every macOS seed
            is a new scenario) and a per-cycle survival check for held paths
            (`heldLost`, shown able to fail); `TestPlanFuzz` gained a `casefold` shape,
            the `pfMints` check, and fails a folder move from a missing source.
            Tried and taken out (rounds 2–4): a held folder renamed here going up as a
            `move_remote` — two regressions; today a new folder with copies, MANUAL
            §6/§8, S039.
            Verification (coordinator, final tree): build, vet, gofmt, Linux build+vet,
            `make test`, `-race` ×9, deep plan fuzz, all five engine sweeps, the full
            crash matrix — green (testing.md S038.62). `make test` is slower: the
            engine package carries the thinned matrix (17 s alone).
            Not closed: one-time failed actions in three shapes and a folder id lost
            on `P/D` → `Q/d` (S037); duplicate folder names on Drive — re-binding, the
            kept folder's edits not uploading, also on Linux (S039); downloads into
            an unreadable folder (S040); status.md's parked directory arm is covered
            except R7-for-dirs with a tracked live folder at the destination (S039).
            Never run: Linux casefold (the two-step rename only through a test seam),
            the engine/executor tests on a case-sensitive disk, real Drive.
            Docs: MANUAL §6/§8/§9, specs §4.2/§4.6/§5/§7, testing.md S038.1–S038.62,
            README + CLAUDE.md (the matrix command).
