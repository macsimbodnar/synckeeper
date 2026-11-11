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
author:
done:
