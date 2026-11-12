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
done:       2026-11-12 — two rounds by one Opus subagent, an independent check after
            each (the second with no finding), coordinated and committed by Claude
            (agent). All nine seeds green; both 200-seed sweeps green with the two
            checkpoints in the list.
            The goal's premise was partly wrong: adding two entries to
            `crashCheckpoints` reshuffled every seed's random stream. With the new
            checkpoints drawn but never fired (`SYNCKEEPER_FUZZ_DISARM`), seeds 52, 92,
            130, 162 still fail — only 56, 66, 91, 122, 128 need the new window. Four
            causes, three of which need no crash:
            A (92, 162; end state of 66, 91, 128) — a folder deleted elsewhere and made
            again got a duplicate on Drive (the old one resurrected beside the new).
            B (56, 122, 66, 91, 128; 52 for a download) — a rename or download done on
            disk with its commit lost; reachable by process death and by a failed DB
            write. Fix: recovery commits what is owed before replanning
            (`executor/resume.go`, `finishOwedCommits`), on evidence only (file: row id,
            size, mtime or md5; download: md5; folder: weaker — any folder at the
            destination, the traced cases self-heal); a `MoveLocal` is journaled
            `applied` between rename and commit (a value of the existing text column,
            no schema change; one more DB write per local rename).
            C (130) — an upload landed in the wrong Drive folder: a new folder's id was
            seeded at a path before the folder rename ran. Seeds under a moving folder
            are applied after the move.
            D (122; 52 after B) — Drive renamed a folder onto a name a local folder
            holds: refused every cycle. Now one rule (specs §4.3, "a Drive folder
            arriving where a folder still sits here"): the arriving folder takes the
            path; the old one's files are judged each on its own (unchanged → bin,
            edited or new → uploaded into the arriving folder); a merge source the plan
            empties is removed here, never created on Drive. Round 2 came from the
            first check: rule A alone refused "D deleted, E renamed onto D" every cycle
            and sent an unsynced edit into the trashed folder — fixed by the same rule,
            plus an executor guard (`LiveFolders`): no upload or mkdir under a folder
            the snapshot lacks or the plan trashes.
            Tests: testing.md S035.1–S035.21, red first except the recovery unit cases
            (new code). `TestPlanFuzz` gains the merge start state; folder identity is
            covered by a six-case engine table, not by the plan fuzz (its model has no
            Drive parents).
            Coordinator's verification, final tree: build, vet, gofmt, Linux build+vet,
            `make test`, `-race` on seven packages, deep `TestPlanFuzz`, 60×150 (202 s),
            crash=1 ×40 (50 s), 200×150 (622 s), crash=1 ×200 (265 s) — all green.
            Not closed: `doctor --repair` clears the journal, so a commit owed at that
            moment falls back to the old replan (MANUAL §8); a file edited before its
            rename was cut gets no recovery (falls back, nothing lost); the emptied
            source's removal is planned from scanned files only (one failed action when
            it holds a symlink, then converges — noted in S036). Never run: real Drive,
            Linux runtime.
            Docs: MANUAL §6/§8, specs §4.3/§4.4/§4.6, testing.md.
