id:         S037
goal:       In a few folder-rename shapes the plan carries actions at a path that an
            earlier action of the same plan moves away, or needs a parent that is not
            there yet: the action fails once ("no such file", "disappeared",
            destination parent missing), the next cycle is correct, nothing is lost.
            One failed action still costs a cycle and, until S022, switches off that
            cycle's purge, deletion report and activity detail. Shapes, all reproduced
            at engine level by S036's independent check (2026-11-12):
            (1) Drive renamed `D`→`N` and `D/E`→`N/F`, this disk made only the inner
                rename (`D/F`): pass 1's actions that use the current disk path come
                out at `D/F/…` (`upload`, `quarantine_local`, a `conflict_backup` or
                pinned `download`) while the plan first moves `D` to `N`. New with
                S036 (before it the whole plan was refused every cycle).
            (2) A plain receiver has the same kind of stale path:
                `quarantine_local D/E/b.txt` fails once. Pre-existing.
            (3) Two machines rename one folder differently and one makes both renames
                in one cycle: one failed `move_local` (destination parent missing).
                Pre-existing. That machine's double rename also reaches Drive as a new
                folder plus a trashed one, not as a rename.
            (4) The folder merge one level down — Drive `D`→`N` and `D/E`→`N/F`; here
                `D/E` plus a new untracked `D/F`: three failed actions (the folder
                move, an `update_remote`, an upload), then it merges. Pre-existing;
                merge detection looks up the destination only at Drive's path
                (testing.md S036.9).
accepts:    Each shape a named test, red first (reconcile where the plan shows it,
            engine with two machines for the end state): zero failed actions, same end
            state as today's second cycle. Actions emitted by pass 1 for a file under a
            folder the plan moves are expressed at the path the file has when they run
            (or ordered before the move). `TestPlanFuzz`'s model executor counts a
            planned action that fails on a clean run as a finding, on all three shapes
            (today it checks lost bytes and refusals, not clean-run failures) — or a
            reason why not. testing.md rows; MANUAL §8 row removed.
touches:    internal/reconcile, internal/engine (tests), MANUAL.md, adocs/specs.md,
            adocs/testing.md
excludes:   S032 (rename evidence), S020. Shape (3)'s Drive-side outcome (new folder +
            trash instead of a rename) is noted, not required here.
closes:
paused_by:
author:
done:
