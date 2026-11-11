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
author:     Opus subagent, coordinated by Claude (agent); started 2026-11-11 at Max's direction (DEC-002)
done:       2026-11-11. After the plan is built and collapsed, the engine pins on every
            folder `TrashRemote` the folder's subtree as the mirror held it, minus what
            the same plan trashes or moves out (`reconcile.PinFolderTrashContents` →
            `RemoteExpect.Contents`). `verifyFolderContents` in the choke-point file
            lists each expected folder on Drive — deepest first, 4 at a time
            (`transferWorkers`), the trashed folder itself last — and refuses with
            `ErrDrift` on any unexpected, missing, moved, renamed or re-contented item.
            No fallback to item-by-item in the same cycle; the next cycle keeps the
            folder and the other machine's file (§4.4). Both shapes (collapsed,
            uncollapsed) covered. No schema change; journal field additive.
            Tests (testing.md S021.1–S021.14): both races × both shapes, subfolder
            added, file trashed / moved out inside, cost control, walk order,
            concurrency vs serial on a 316-folder tree; a skipped test pins the window.
            Two rounds, one fast check (REVIEW): no permanent refusal in normal plans,
            no false pass; round 2 added the order and the concurrency it asked for.
            Verified by the coordinator after each round: `go build && go vet`,
            `gofmt -l`, `go test -count=1 ./...`, `-race` (executor ×3) green on darwin;
            `GOOS=linux go build/vet` clean; 60×150 fuzz fails on seeds 1, 28, 45 only.
            Cost, accepted and stated in MANUAL §6 / specs §7: one `List` per folder of
            the deleted subtree (before: none) — minutes for thousands of folders.
            NOT closed: a write landing after a folder's listing and before the trash
            (MANUAL §6); an edit to a Google-native file inside is not detected (no
            md5). NOT verified, all → S019: real Drive at all; `files.list` consistency
            right after a write; multi-parent legacy items (would refuse every cycle).
