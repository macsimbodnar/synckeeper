id:         S035
goal:       After a local rename applied from Drive (`MoveLocal`, file or folder) whose
            state-DB commit is lost — the rename is on disk, the row still at the old
            path — later cycles can leave machines with different content for one file,
            a file missing on one machine, a plan refused every cycle (§4.5), or a
            baseline row with no mirror row. PRE-EXISTING: S031 added the fuzzer
            checkpoints `move_before_commit` and `forget_before_commit`, a window never
            cut before; 9 of 400 long runs fail, and all 9 fail the same way on an export
            of `e27c8bc` with only the two checkpoints added (coordinator re-ran them,
            2026-11-12).
            Seeds, `SYNCKEEPER_FUZZ_SEED=<n> SYNCKEEPER_FUZZ_RUNS=1` plus:
            - `SYNCKEEPER_FUZZ_STEPS=150`: 66 (file count a vs b 35/34), 91 (content
              differs at `dir72/f1.txt`, silent; traced by S031's implementer to a folder
              `MoveLocal` cut at its commit followed by Drive moves — R16's area),
              130 (no convergence in 80 rounds at `e27c8bc`; file count a vs b 30/29
              on S031's tree).
            - `SYNCKEEPER_FUZZ_CRASH=1`: 52, 56 (§4.5 overlap, download/update + upload
              on one path), 92, 162 (baseline row with no mirror row), 122 (no
              convergence), 128 (file count 24/23).
            Not minimized. Not known: which are one defect. The fuzzer's hook fails ONE
            action and lets the rest of the plan run — a failed DB write (e.g. disk
            full), not a process death. Recovery is discard-and-replan (specs §4.6), so
            a process death leaves the same state (renamed on disk, row at the old path)
            minus the rest of that cycle: classify each seed by which of the two it
            needs.
accepts:    Each seed minimized to a named test, red first; classified as reachable by
            process death, by a failed DB write, or both. Fixed so that no machine loses
            or mis-binds bytes and the tree converges. Both 200-seed sweeps
            (`SYNCKEEPER_FUZZ_RUNS=200 SYNCKEEPER_FUZZ_STEPS=150`,
            `SYNCKEEPER_FUZZ_CRASH=1 SYNCKEEPER_FUZZ_RUNS=200`) green with the two
            checkpoints in `crashCheckpoints`. MANUAL §8 row removed; testing.md rows.
touches:    internal/reconcile, internal/executor, internal/engine (fuzzer), possibly
            internal/statedb (journal recovery), MANUAL.md, adocs/specs.md, adocs/testing.md
excludes:   S032 (rename evidence standard), S020 (rename vs delete outcome). The
            case-fold non-convergence of crash=1 seed 155 (flaky at `e27c8bc`, 1 of 6
            runs) is a different defect: note it here if met, do not fix it in this step.
closes:
paused_by:
author:     Opus subagent, coordinated by Claude (agent); started 2026-11-12 (DEC-002)
done:
