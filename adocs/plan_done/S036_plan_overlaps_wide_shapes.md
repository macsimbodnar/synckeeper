id:         S036
goal:       Plans still refused every cycle (§4.5 transfer overlap → `stuck`) in shapes
            the permanent plan sweep (`TestPlanFuzz`, S031) does not generate.
            PRE-EXISTING: S031's second fast check widened its generator — two folder
            levels (`D→N` and `D/E→D/F` on Drive, each done locally or not), held rows
            (Drive duplicates hidden from the snapshot, unreadable files), local renames
            replacing a synced file, chains of up to five files with edits — and 1,014
            of 60,000 inputs are refused for an overlap, every one refused at `e27c8bc`
            too. No lost bytes, no O1 refusal in that sweep. Not minimized; not known how
            many distinct causes.
            The reviewer's harness is kept, not compiled, at
            `internal/reconcile/testdata/s036_review_harness.go.txt` (generator `zgen2`,
            counter `TestZZCounts`); copy it beside `planfuzz_test.go` as `_test.go` to
            run it.
            Two notes from the same review, to settle here:
            - its "update_remote uploads a file from another origin" check was left out
              of `TestPlanFuzz` because it flags S023's decided rule; refined (origin may
              be Drive's path or the folder-rewritten path) it catches real wrong moves
              (at `e27c8bc`: folder seeds 5671, 22476) — add it refined, or say why not;
            - folder seed 5671's plan carries `forget h.txt id=I2` twice (moves stage and
              deletes) — harmless, remove the duplicate.
            - from S035's second check: the removal of a merge source the plan empties
              (`quarantine_local`, a folder with no row) is decided from scanned files
              only; a source still holding a symlink or another unscanned entry gets one
              failed action ("directory not empty"), then converges as a new folder —
              do not plan the removal when anything unscanned sits under the source.
            - S035's author thought merge rule D may share causes with these overlaps;
              not checked.
accepts:    The wide generator is a third shape of `TestPlanFuzz`; each distinct overlap
            cause has a named test, red first; deep run
            (`SYNCKEEPER_PLANFUZZ_RUNS=100000`) reports zero refusals and zero losses on
            all three shapes; the engine fuzzer's sweeps stay as they are (S035's seeds
            apart). The testdata file removed once adopted. testing.md rows; MANUAL §7/§8
            if a user-visible claim changes.
touches:    internal/reconcile, adocs/testing.md, possibly adocs/specs.md, MANUAL.md
excludes:   S035 (lost move commit), S032 (rename evidence standard), S020.
            Met by the reviewer, pre-existing, not this step unless it falls out: a plan
            can move a Drive item onto a name another item still holds and trash that
            occupant only in the delete stage (folder seed 3059); a death in between
            leaves two items under one name on Drive.
closes:
paused_by:
author:     Opus subagent, coordinated by Claude (agent); started 2026-11-12 (DEC-002)
done:       2026-11-12 — one round by one Opus subagent, one independent check with no
            finding, coordinated and committed by Claude (agent).
            Recount at `f928b13` (after S035): still 1,014 of 60,000, and ONE cause, not
            several: Drive renamed `D`→`N` and `D/E`→`N/F`, this disk made only the
            inner rename (`D/F`). The done-folder-move detection looked for the
            destination only at Drive's path, so it planned downloads at `N/F/*` and
            uploads of `D/F/*` as new files at the same paths. Fix (`plan.go`): both
            ends of a done folder move are looked up where they sit on disk now, outer
            moves first; S002's evidence rule unchanged. The inner rename is then "the
            same rename on both sides", one level down: a file edited here keeps its
            Drive id (before: uploaded as new, the old item trashed). After: 0 of
            60,000; deep run 100k × three shapes: 0 refusals, 0 losses.
            The notes: the `update_remote` origin check is in `TestPlanFuzz`, refined
            (0 flags now; 394 of 30,000 at `e27c8bc`); a row is forgotten once (the
            duplicate was in 1,099–1,427 of 60k plans per shape, harmless); a merge
            source holding anything unscanned is a new folder, not a failed removal.
            The wide generator is `pfGenWide`, third shape of `TestPlanFuzz`; the
            testdata harness is removed. S035's merge rule shares no cause with these.
            Tests: testing.md S036.1–S036.11, red first.
            Coordinator's verification: build, vet, gofmt, Linux build+vet, `make
            test`, `-race` ×7, deep plan fuzz, all five engine sweeps — green.
            Not closed → S037: actions planned at a path the same plan moves away, or
            under a parent not yet there — fail once, next cycle correct, nothing lost
            (the done-inner-rename shape, new with this step in place of the refusal;
            a plain receiver; two different renames of one folder; the merge one level
            down). The Drive-side duplicate-name window (folder seed 3059) untouched.
            Never run: real Drive, Linux runtime.
            Docs: MANUAL §6/§8, specs §4.3/§4.5, testing.md.
