id:         S039
goal:       A machine that changed nothing loops forever when Drive's folders are
            (1) swapped by id — `D` ↔ `E` — it plans `move_local D->E` and `E->D` and
            fails every cycle (S031 fixed swaps for FILES, through a temporary name;
            folder moves are not in that ordering); (2) renamed so that two Drive
            folders hold one name in one parent (the web UI allows it): the receiver
            never settles. PRE-EXISTING (same at `5a935a5`); reproduced by S020's
            independent checks (2026-11-12, machine "B" in the probes). Nothing is
            lost in the probes. Since S020 the machine that DELETED one of the folders
            is clean in both; the plain receiver is not.
            Related gap, from S020's implementer, by reading: a shadowed folder's rows
            are held by its Drive path only (R24, `expandShadowed`) — when the
            duplicate that wins is the OTHER folder, the loser's rows under their old
            local path are not held and are forgotten; they come back once the
            duplicate is resolved.
            Measured by S038's second independent check (2026-11-12; same at
            `14ed882`): Drive gains an exact-duplicate folder `D` that wins by id →
            every machine plans `mkdir_local D; download D/z.txt`, which re-binds row
            `D` to the newcomer's id and pulls its files into our folder. A later
            rename `D` → `D2` here then moves the NEWCOMER on Drive, the user's own
            files end back at `D`, and the other machine fails 7 actions in one cycle;
            a case-only rename `D` → `d` in that state leaves a fold pair or exact
            duplicates on Drive.
            Measured by S038's third independent check (same at `14ed882`): an exact
            duplicate `D` that LOSES to our synced `D` still holds every row of the
            winner (the loser's skip path prefix-matches them, `expandShadowed`) — an
            edit to `D/x.txt` here is never uploaded, reported "shadowed" every cycle.
            Tried in S038 and taken out again: "a held folder renamed here goes up as
            one `move_remote`, its Drive folder kept" (today: a new folder with
            copies; Drive keeps both old ones; the hold stays). It regressed twice —
            a deleted held folder's rows, taken as pairing sources, stole the evidence
            of an unrelated folder rename (`F` → `G` ended as `mkdir_remote G` +
            `trash_remote F`); and when the winning duplicate arrived in the same
            cycle its files were downloaded into the renamed folder and the rows
            re-bound. If built here, both shapes are acceptance tests.
accepts:    Red-first, engine level: (1) a folder swap and a three-folder rotation made
            on Drive by id apply on a receiver with zero failed actions (dependency
            order / a temporary name, as S031 does for files), crash-safe at each step
            (`move_before_commit`); (2) two Drive folders under one name: the receiver
            settles — the loser held and reported (§5), its rows held wherever they
            sit, nothing trashed, nothing looping; a row is never re-bound to another
            Drive folder of the same name (the winner arriving beside a synced folder
            is the one held, or the synced one is — one rule, ids never swapped under
            a folder's files). `TestPlanFuzz`'s folder shapes gain
            folder swaps if affordable. MANUAL §8; specs §4.5/§5; testing.md.
touches:    internal/reconcile (folder-move ordering, holds), internal/executor,
            tests, MANUAL.md, adocs/specs.md, adocs/testing.md
excludes:   S038 (case-fold folders), S037 (actions at pre-move paths).
closes:
paused_by:
author:
done:
