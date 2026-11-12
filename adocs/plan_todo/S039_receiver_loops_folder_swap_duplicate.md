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
accepts:    Red-first, engine level: (1) a folder swap and a three-folder rotation made
            on Drive by id apply on a receiver with zero failed actions (dependency
            order / a temporary name, as S031 does for files), crash-safe at each step
            (`move_before_commit`); (2) two Drive folders under one name: the receiver
            settles — the loser held and reported (§5), its rows held wherever they
            sit, nothing trashed, nothing looping. `TestPlanFuzz`'s folder shapes gain
            folder swaps if affordable. MANUAL §8; specs §4.5/§5; testing.md.
touches:    internal/reconcile (folder-move ordering, holds), internal/executor,
            tests, MANUAL.md, adocs/specs.md, adocs/testing.md
excludes:   S038 (case-fold folders), S037 (actions at pre-move paths).
closes:
paused_by:
author:
done:
