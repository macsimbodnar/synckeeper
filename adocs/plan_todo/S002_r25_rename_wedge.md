id:         S002
goal:       Fix the R25 crash-rename wedge: a local directory rename can put one
            rel_path in the plan as both upload and download, `ValidateTransferStage`
            refuses the whole plan, and since a refused plan changes no state the
            next cycle plans the same thing — the machine stops syncing until
            something external changes.
accepts:    Seeds 19 and 2 pass:
            `SYNCKEEPER_FUZZ_SEED=19 SYNCKEEPER_FUZZ_RUNS=1 SYNCKEEPER_FUZZ_CRASH=1
            go test ./internal/engine -run TestFuzzConvergence`. Seed 45 passes at the
            default crash probability (audit 2026-11-11_adversarial F09: `SYNCKEEPER_FUZZ_RUNS=60
            SYNCKEEPER_FUZZ_STEPS=150` is red on it, so the wedge does not need
            `SYNCKEEPER_FUZZ_CRASH=1`); the records saying it does — decisions.md:60,
            plan_history.md:186, MANUAL §8 — are corrected in the fix's commit. A fuzzer sweep at
            crash probability 1.0 becomes a recorded sweep so the class cannot
            regress unseen. testing.md rows land with the fix; MANUAL §8's
            known-bugs entry leaves the list in the fix's commit.
touches:    internal/reconcile (pass 1, red-first before the planner), internal/engine,
            adocs/testing.md, MANUAL.md
excludes:   Nothing is implemented before the shape is decided with Max: a
            rejected-plan-is-permanent wedge may deserve a general answer (a refused
            plan repeating identically is a reportable standstill) rather than one
            more special case in pass 1. Both were named at triage; neither is chosen.
closes:     2026-11-11_adversarial-F09
paused_by:
author:
done:
