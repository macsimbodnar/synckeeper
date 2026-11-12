id:         S002
goal:       Fix the R25 crash-rename wedge: a local directory rename can put one
            rel_path in the plan as both upload and download, `ValidateTransferStage`
            refuses the whole plan, and since a refused plan changes no state the
            next cycle plans the same thing — the machine stops syncing until
            something external changes.
            NO CRASH NEEDED (found 2026-11-11 by S023's implementer, reproduced with
            a scratch two-machine test at `c70a5ae`, not re-run by the coordinator):
            both machines rename folder `D` → `N`, then b edits `N/f.txt` and syncs;
            a's next cycle fails with `plan violates §4.5: download and upload both act
            on "N/f.txt"`, every cycle (5 checked); the same when both edit. The remote
            dir move makes `remoteMoved` false, so it is not pass 1's coincident-move
            branch. This falsifies "needs crashes" outright — the wedge is reachable in
            ordinary two-machine use.
accepts:    The no-crash shape above passes as a red-first engine test. Seeds 19 and 2 pass:
            `SYNCKEEPER_FUZZ_SEED=19 SYNCKEEPER_FUZZ_RUNS=1 SYNCKEEPER_FUZZ_CRASH=1
            go test ./internal/engine -run TestFuzzConvergence`. Seed 45 passes at the
            default crash probability (audit 2026-11-11_adversarial F09: `SYNCKEEPER_FUZZ_RUNS=60
            SYNCKEEPER_FUZZ_STEPS=150` is red on it, so the wedge does not need
            `SYNCKEEPER_FUZZ_CRASH=1`); seeds 1 and 28 fail the same sweep with the same message, measured 2026-11-11 at `06df839` — all three must pass; MANUAL §8's row leaves in the fix's commit
            (the archive records are corrected by DEC-004, not edited). A fuzzer sweep at
            crash probability 1.0 becomes a recorded sweep so the class cannot
            regress unseen. testing.md rows land with the fix; MANUAL §8's
            known-bugs entry leaves the list in the fix's commit.
touches:    internal/reconcile (pass 1, red-first before the planner), internal/engine,
            adocs/testing.md, MANUAL.md
excludes:   Shape decided by Max 2026-11-11 (DEC-004), both parts: (1) the planner is fixed
            for every known route — no rel_path, nor an ancestor/descendant pair, carries
            two transfer-stage actions; (2) a plan `ValidateTransferStage` refuses cycle
            after cycle is shown in `status` as a standstill. The archive entries that
            said "only at `SYNCKEEPER_FUZZ_CRASH=1`" (decisions.md 2026-08-01,
            plan_history.md W22) are NOT edited — both are read-only; DEC-004 corrects
            them. Out of scope: S020's rule (folder deleted here vs renamed there).
closes:     2026-11-11_adversarial-F09
paused_by:
author:     Opus subagent, coordinated by Claude (agent); started 2026-11-11 at Max's direction (DEC-002, DEC-004)
done:       2026-11-12. Both parts of DEC-004 built; three rounds, three fast checks.
            PART 1 — four causes of one class fixed in `internal/reconcile/plan.go`:
            (A) a remote folder move the disk already shows — both machines renamed
            D→N — reads D's rows at their files under N; (B) lookups under a locally
            renamed folder go through the inverse of the planned reparent; (C) the R25
            branch and the move-destination occupant check find the local file that
            will land at a path; (D) R25 applies when the row at the destination lost
            its Drive item (found by widening the fuzzer: seed 124 at crash=1 ×200).
            Evidence rule (round 2/3): under a done folder move a local file stands for
            the row only by content — its bytes equal the baseline's or Drive's, or at
            least one NON-EMPTY file of D sits under N unchanged; otherwise the row is
            forgotten FIRST and both files are left alone, so the next cycle keeps both
            (conflict copy) and no interruption can read Drive's item as deleted.
            PART 2 — `ValidateTransferStage` returns a typed `*TransferOverlapError`;
            the daemon counts consecutive refused cycles in memory and publishes an
            additive `standstill` field in `stat`; `status` (dashboard, `--plain`,
            `--json`) shows "stuck" with since / cycles / paths. No schema change.
            Tests: 48 by name (testing.md S002.1–S002.28), incl. route 1's four
            variants, S1/S2 (unrelated folder), every-interruption crash tests.
            FUZZ, re-run by the coordinator on the final tree, all green: default;
            60×150 (was red on 1, 28, 45); crash=1 ×40 (was red on 2, 19); 200×150;
            crash=1 ×200. Also `go build && go vet`, `gofmt -l`, `go test -count=1
            ./...`, `-race` on six packages, `GOOS=linux go build/vet`.
            NOT closed — the third fast check, MANUAL §8 carries each, → S027:
            a chain rename on Drive clobbers an unsynced local edit (F4 — PRE-EXISTING,
            same plan on `5f61255`: file moves are ordered by depth and path, not by
            dependency, and an edited tracked occupant is treated as safe on Drive);
            the same through the forget arm in a compound shape (F1); one compound
            shape still refused every cycle (F2, now visible as stuck); the forget is
            not a protector of the folder commit, so a failed DB write lets the commit
            through (F3, read). Also open: a swap made by id on Drive fails its move
            commits for a cycle or two (pre-existing); a folder rename differing only in
            case loses the ids (needs the Parked directory arm). Owner's call, → S028:
            how strong rename evidence must be — one identical non-empty file suffices
            today, here and in `detectLocalDirMoves` (pre-existing: there an EMPTY
            paired file suffices, confirmed on `5f61255`). Noted: a flaky race in
            `internal/tui` under `-race` (→ S029); `delegateSync` may print `Done.` for
            a refused cycle (read, → S030). README status line no longer names steps.
