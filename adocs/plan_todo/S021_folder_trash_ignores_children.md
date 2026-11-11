id:         S021
goal:       A folder trash does not look inside the folder. The remote-write gate (S010)
            pins and re-checks the folder itself — parent, name, not trashed — never
            its children, so a file edited or added on Drive inside a folder this
            machine deletes, after its refresh and before its `Trash`, goes to Drive's
            bin with the folder and then off the other machine's disk. The part of
            audit 2026-11-11_adversarial F04's class S010 left open; reproduced on the
            fake by S010's implementer (edit case and add case). The local side already
            has the counterpart: a collapsed folder delete carries `Subtree` and
            `verifySubtree` refuses on anything the plan never saw (W13).
accepts:    Red-first engine tests for both races (a file edited inside, a file added
            inside, between this machine's refresh and its folder trash): the folder
            trash is refused, the next cycle keeps the edited/added file on every
            machine. The folder's remote subtree as the plan saw it is compared against
            Drive immediately before the trash. API cost stated in specs §7 (listing
            calls per folder trash). testing.md rows; MANUAL §8 row leaves the list.
touches:    internal/reconcile, internal/executor, internal/driveclient, MANUAL.md,
            adocs/specs.md, adocs/testing.md
excludes:   The real-client window and retry questions (S019). Folder deleted here vs
            renamed there (S020).
closes:
paused_by:
author:
done:
