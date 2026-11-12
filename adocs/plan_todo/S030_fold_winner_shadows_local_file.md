id:         S030
goal:       On a case-insensitive sync folder, a Drive newcomer that wins the fold no
            longer wedges a download against the file already on disk. Case: the folder
            holds `Readme.txt` (baselined, Drive id B); Drive gains `README.txt` (id A,
            from a case-sensitive machine or the web UI) and A < B. `remotedelta.Snapshot`
            (`internal/remotedelta/remotedelta.go:323`) keeps the lowest id per fold key,
            so A wins and B is fold-skipped; reconcile holds B's baseline row (R19d) but
            plans `Download README.txt` with `LocalExists:false` — its local lookup is by
            exact bytes (`internal/reconcile/plan.go` pass 3, ~778–810). Confirmed by a
            probe test (2026-11-12, fast check of S001, scratch copy; not in the repo):
            Base `Readme.txt`/B + Local `Readme.txt` + Remote `README.txt`/A +
            `ShadowedRemote{B}` + `CaseFold` → that download. Then, by reading only: the
            executor's overwrite guard (`executor.go:764` → `guardedStat`,
            `localwrite.go:60`) `Lstat`s `README.txt`, finds `Readme.txt` on a folding
            disk and refuses ("a file appeared after the scan") — every cycle, with a
            fresh temp download each time. No data loss; one failed action per cycle,
            which until S022 also switches off purge, deletion report and activity
            detail. Pre-existing since the 2026-07-14 case-collision decision; R19's
            tests cover the shadowed row with no winner present. Found by the fast check
            of S001's close-out (Mac + Linux now share one Drive folder in the field).
accepts:    Reproduced first, on a real case-insensitive directory: `pop-os` has one
            without sudo — `unshare -Urm`, `mount -t tmpfs -o casefold tmpfs <dir>`,
            `mkdir <dir>/d && chattr +F <dir>/d` (subdirs inherit; verified 2026-11-12) —
            or on APFS. A failing test, then the fix: the folding machine plans no action
            that targets a path held by a different local file under the same fold key;
            the winner is a reported skip — DEC-011 (Max, 2026-11-12): named as a
            name conflict, a distinct typed skip kind carrying both names so a later
            UI can show it, surfaced wherever skips surface today — the existing file
            is untouched, and the cycle has no failed action. Directory flavour noted, not built (parked "directory
            arm"). MANUAL §8 row removed in the fix's commit; §9 mixed case-sensitivity
            bullet re-checked.
touches:    internal/remotedelta and/or internal/reconcile; tests; MANUAL.md
excludes:   the directory arm (parked, decisions.md "W1.9.1"/"W1.8.8"); normalization
            folding unless the same fix covers it for free.
closes:
paused_by:
author:
done:
