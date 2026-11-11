id:         S023
goal:       When both sides rename one file to the same new name and only the local
            content changed, the planner makes a conflict instead of an upload: the R25
            path (`internal/reconcile/plan.go:252-272`) conflicts whenever
            `ln.MD5 != ra.item.MD5` and never asks whether the remote content is still
            what the baseline row recorded (`ra.item.MD5 == b.DriveMD5` → local edit
            wins, `UpdateRemote`). Drive's older bytes keep the name and the local edit
            becomes the conflict copy. Pre-existing; reproduced by S011's fast check
            (2026-11-11) in the shape where it hurts: A's upload went partial, B
            renamed `video.mkv` → `final.mkv`, A renamed it the same way before
            re-uploading → `final.mkv = PART-1|PART-2|` and the complete file under the
            conflict name on a, b and Drive.
accepts:    A red-first reconcile test and an engine test with that shape: one
            `final.mkv`, complete, everywhere, no conflict copy. The same without S011
            in play (A renames and edits, B only renames). A true double edit under a
            double rename still conflicts. MANUAL §6's exception clause leaves in the
            fix's commit; testing.md rows; specs §4.2/§4.3.
touches:    internal/reconcile (pass 1, R25 path), internal/engine, MANUAL.md,
            adocs/specs.md, adocs/testing.md
excludes:   The R25 wedge itself (S002) — same code path, different defect; if S002's
            shape is decided first, build this on top of it.
closes:
paused_by:
author:     Opus subagent, coordinated by Claude (agent); started 2026-11-11 at Max's direction (DEC-002)
done:       2026-11-11. Pass 1's coincident-move branch (R25) decides by §4.2's notions
            (`localChanged := ln.MD5 != b.MD5`, `remoteContentChanged := ra.item.MD5 !=
            b.DriveMD5`): equal → `Record`; local only → `UpdateRemote` on the same id
            (pinned, `RemoteWant`); remote only → `Download` over the local file (pinned
            to its scanned stat) — the mirror case, beyond this step's text, kept by
            Max; both → the conflict as before. DEC-003 records Max's choice of this
            rule over the recorded conflict. `TestR25CoincidentMoveDivergentContentConflicts`
            re-targeted to a true double edit (not deleted); testing.md R25 says so.
            New tests: 6 reconcile + the pin case + 4 two-machine engine tests, incl.
            the reproduced S011 shape — one complete `final.mkv` everywhere, no conflict
            copy (testing.md S023.1–S023.9). MANUAL §6's exception clause removed.
            Verified by the coordinator: `go build && go vet`, `gofmt -l`,
            `go test -count=1 ./...`, `-race` on reconcile/engine/executor green on
            darwin; `GOOS=linux go build/vet` clean; fuzz sets unchanged — 60×150:
            seeds 1, 28, 45; `SYNCKEEPER_FUZZ_CRASH=1` ×40: seeds 2, 19 (all S002).
            Fast check (REVIEW): both arms sound (an empty or stale `DriveMD5` can never
            read as "remote unchanged"; the download is behind the local write gate),
            no §4.5 overlap, next cycle idle. One consequence added to specs §4.3 on its
            finding: a file deleted here plus an unrelated file created at the name
            Drive renamed it to is this same shape → one update, the old content only in
            Drive's revisions (before: a conflict keeping both). Coverage gaps, noted:
            the fuzzer never reaches the new arms; nothing drives the crashed-`MoveLocal`
            route through them. Found while working, not fixed (→ S002): the §4.5 wedge
            is reachable without a crash.
