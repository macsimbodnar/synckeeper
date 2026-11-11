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
done:
