id:         S016
goal:       Linux: the mass-delete guard asks whether the *home* trash is available
            (`internal/trash/trash_linux.go:224-234`) but a sync dir on another
            filesystem deletes into `<mount>/.Trash-$uid` (`:137-162`). Where that is
            unwritable every delete falls to the quarantine with the guard believing a
            bin exists — a mass deletion from Drive runs unheld (INV-4). Found by
            audit 2026-11-11_adversarial (F11, Medium, code reading only).
accepts:    Reproduced first on Linux hardware (sync dir user-owned inside a root-owned
            mount) — the finding is confirmed or corrected on that run. Then the guard
            probes the trash the sync dir will actually use, with a red-first test.
            testing.md row lands with the fix; MANUAL §8 row leaves the list in the
            fix's commit.
touches:    internal/trash (linux), internal/engine, MANUAL.md, adocs/testing.md
excludes:   Runs with or after S001's L8 hardware gates — same machine.
            HOW TO REPRODUCE (for the session on Linux): a root-owned mount holding a
            user-owned sync dir, e.g. `sudo mkdir -p /mnt/sk_f11 && sudo mount -t tmpfs
            tmpfs /mnt/sk_f11 && sudo mkdir /mnt/sk_f11/sync && sudo chown $USER
            /mnt/sk_f11/sync` (the mount root stays root-owned, so
            `/mnt/sk_f11/.Trash-$UID` cannot be created); point a test engine's sync dir
            there (a Linux-tagged test using the in-memory Drive fake, skipped unless an
            env var names the prepared directory); delete >25% and >10 of the tracked
            files on the fake; expected today: `trash.Available()` true (home trash), so
            the mass-delete guard does not hold, every `MoveToTrash` fails, files fall
            to the quarantine. Unmount with `sudo umount /mnt/sk_f11` afterwards.
closes:     2026-11-11_adversarial-F11
paused_by:
author:     Claude (agent), started 2026-11-12 on `pop-os` at Max's direction
done:       2026-11-12, on `pop-os`. F11 reproduced on hardware first and confirmed as
            written: real tmpfs, mount root unwritable, 40 of 41 files binned in Drive →
            `TrashAvailable=true`, guard silent, 40 files to the quarantine. No `sudo` in
            the session, so a user namespace (`unshare -Urm`, `chmod 555`, capabilities
            dropped) instead of the `sudo` recipe — same mechanism, uid 0 inside.
            Fix: `trash.AvailableFor(dir)` probes the bin an item inside `dir` reaches
            (`trashDirIn`; creates `files/`+`info/` as a move would, `access(W_OK|X_OK)`);
            `Trasher` has no dir-less probe, so the engine guard and both executor
            removal roads ask `AvailableFor(SyncDir)`. Tests: engine
            `TestF11GuardHoldsWhenTheSyncDirsVolumeTrashIsUnusable` (gated,
            `SYNCKEEPER_F11_DIR`) red before / green after; trash
            `TestAvailableForProbesTheTrashTheDirUses` (runs in `make test` on Linux).
            Red-checks: home-trash probe → both red; `access` removed → read-only arm
            red. `make test`, `go vet ./...`, `gofmt -l`, `-race` on trash/executor/
            engine/guards green on linux; `GOOS=windows go build`, `GOOS=darwin go vet`
            clean. Fast check (REVIEW): no defect; its nested-mount edge recorded
            (testing.md S016.6, MANUAL §8). Residual: `status`/`doctor`/`info`/daemon
            start line still name the home bin → S028; MANUAL §8 row rewritten to say
            so (not removed — the step's "row leaves the list" holds for the guard).
