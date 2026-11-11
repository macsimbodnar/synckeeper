id:         S025
goal:       The routes S013 left open, by which a synced folder swapped for a symlink is
            still trashed on Drive and removed from other machines (content behind the
            link untouched). (1) the link is renamed, or replaced by a differently named
            link; (2) the link's parent folder is renamed and nothing beside the link
            lets the planner detect the rename; (3) parent and link both renamed —
            (1)–(3) reproduced in planner tests (`TestARenamedLinkIsADeletion`,
            `TestAnUndetectedParentRenameOfAHeldLinkIsADeletion`); (4) the swap done in
            steps while the daemon runs: the folder is deleted before the link exists, a
            cycle runs in the gap (500 ms debounce, `internal/watch/watch.go:84`) and
            reads a real deletion — from code reading, and probably the commonest real
            route. The hold is by path and has no memory: nothing records that a row was
            held, so a held path that changes name is indistinguishable from a deletion.
            Same root cause, same step — the unreadable arm S014 left open (both
            reproduced in the planner): (5) a file renamed AND edited or touched before
            it became unreadable, so S014's (size, mtime) identity match no longer
            holds its old row (`TestAnUnreadableFileWithAnotherStatHoldsNothingElse`);
            (6) a folder renamed while holding an unreadable subfolder and nothing
            readable beside it (`TestAnUnreadableSubdirUnderAnUndetectedRenameIsADeletion`).
            A "same name anywhere" guess was built and removed in S013 (it turned moves
            into copies in any tree with unrelated links). Part of audit
            2026-11-11_adversarial F07.
accepts:    Max decides first whether this is worth closing at all, and how: remember
            held rows in the state DB (schema change) so a renamed link keeps its hold;
            and/or delay propagating a large local folder deletion long enough for a
            link to appear; or accept the residual as documented. Whatever is chosen
            lands with red-first tests for routes (1)–(4); MANUAL §8 row leaves or is
            narrowed in that commit.
touches:    internal/reconcile, internal/statedb, internal/watch, MANUAL.md,
            adocs/specs.md, adocs/testing.md
excludes:   Following symlinks. Deleting the link stays a deletion.
closes:
paused_by:
author:
done:       2026-11-11. NOT BUILT — closed by decision, not by work: Max chose to accept
            the residual (DEC-009); the residual routes stay open and documented (MANUAL §8).
