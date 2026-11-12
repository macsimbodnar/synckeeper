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
            ALSO THIS STEP (added 2026-11-12, same family — a folding machine planning an
            action onto a path held by a different local file under the same fold key):
            (b) machines b, c synced; c renames `db/f0.txt` → `da/f0.txt`; b, on a
            folding disk, writes a new `da/F0.txt`. b uploads `da/F0.txt` (Drive now
            holds both names) and fails `move_local db/f0.txt -> da/f0.txt` ("a file
            appeared after the scan") every cycle; c never gets `da/F0.txt`. Nothing
            lost. Reproduced on `5a935a5` (coordinator, S020's close-out). **It makes
            the deep engine fuzz red at HEAD since S020: `SYNCKEEPER_FUZZ_RUNS=200
            SYNCKEEPER_FUZZ_STEPS=150`, seed 102** (`dir44/F0.txt` vs `dir44/f0.txt`,
            no convergence) — S020 changed that seed's path, not the defect.
            (c) crash=1 ×200 seed 155, flaky (1 of 6 runs at `e27c8bc`): Drive holds
            fold-equal `dir5/F0.txt` and `dir5/f0.txt`, a folding machine plans
            `download dir5/f0.txt` every round — this step's main shape.
accepts:    Reproduced first, on a real case-insensitive directory: `pop-os` has one
            without sudo — `unshare -Urm`, `mount -t tmpfs -o casefold tmpfs <dir>`,
            `mkdir <dir>/d && chattr +F <dir>/d` (subdirs inherit; verified 2026-11-12) —
            or on APFS. A failing test, then the fix: the folding machine plans no action
            that targets a path held by a different local file under the same fold key;
            the winner is a reported skip — DEC-011 (Max, 2026-11-12): named as a
            name conflict, a distinct typed skip kind carrying both names so a later
            UI can show it, surfaced wherever skips surface today — the existing file
            is untouched, and the cycle has no failed action. Shape (b): the local
            newcomer and the incoming move meet the same rule — no upload that mints a
            case-sibling of a name Drive holds or this plan moves there; reported
            skip; zero failed actions; the 200×150 sweep green again (seed 102).
            Directory flavour: S038. MANUAL §8 row removed in the fix's commit; §9 mixed case-sensitivity
            bullet re-checked.
touches:    internal/remotedelta and/or internal/reconcile; tests; MANUAL.md
excludes:   the directory arm (parked, decisions.md "W1.9.1"/"W1.8.8"); normalization
            folding unless the same fix covers it for free.
closes:
paused_by:
author:     Opus subagent, coordinated by Claude (agent); started 2026-11-12 (DEC-002)
done:
