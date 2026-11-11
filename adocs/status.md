# Status

Convenience view, rewritten by hand at the end of any turn that changed plan
state. The filesystem beats this file: on disagreement, `plan_current/` wins
and this file is rewritten to match it.

Updated: 2026-11-11, by Claude (agent) at Max's direction

- **Audit 2026-11-11 (adversarial, commit `1cea1b7`): 11 findings, all `planned`.** Report: [audit/2026-11-11_adversarial.md](audit/2026-11-11_adversarial.md). Steps S007–S016, plus F09 onto S002. Eight gated repro tests (`SYNCKEEPER_AUDIT_REPRO=1`); each fix removes its test's gate. MANUAL §8 carries every finding as a known bug until its fix lands.
- **Fix campaign authorized by Max 2026-11-11 (DEC-002):** audit bugs first, in `plan.md` order; distribution (S012, the OAuth client question) deferred and uncertain. One Opus subagent implements each step, sequentially; the main session coordinates, reviews, and commits.
- **The R25 wedge needs no crash** (S002): both machines rename a folder the same way, one edits a file inside → the other wedges every cycle. Reproduced by S023's implementer; MANUAL §8 corrected. S002 is no longer a rare-crash defect — worth moving up among the steps that wait on Max.
- **In progress: S002** (the §4.5 rename wedge — planner fix + visible standstill, DEC-004).
- **Done 2026-11-11, one commit each, a fast check on each:** S007–S011, S013, S014, S018, S021, S023, S026. `make test` green at every commit. S010's, S011's and S021's real-client paths have never run against real Drive.
- **Max's decisions, 2026-11-11 (DEC-003..DEC-009):** S023 → new rule; S002 → planner + visible standstill; S017 → marker file in the sync folder; S020 → rename beats delete; S022 → partial upload stays failed, gates fixed; **S024 and S025 → not built, residuals accepted** (in `plan_done/`, stamped NOT BUILT; MANUAL §8 keeps both rows). Live-Drive tests authorized ("Do il permesso al test").
- **Next by plan order:** S020, S017, S022 (agents, this machine); then S019 and S015 (live Drive, this machine's token, a throwaway Drive folder only).
- **FOR THE SESSION ON LINUX (`pop-os`) — Max starts it; work on a branch `linux`, not on `master`, so it cannot collide with the fixes landing here:**
  1. `git pull` — HEAD must contain `b7e65bf` or later.
  2. **S001's L8 gates**, exactly: `go build ./... && go vet ./... && go test ./... && go test -race ./...`; `SYNCKEEPER_SCALE_FILES=50000 go test ./internal/watch/ -run 'TestScale|TestFSEventsScale' -timeout 20m` (expect ≈0 unwatchable directories); the 2 h soak detached with suspend inhibited: `nohup systemd-inhibit --what=sleep env SYNCKEEPER_SOAK_SECONDS=7200 go test ./internal/watch/ -run TestSoak -timeout 3h -v > soak.log 2>&1 &` (pass = the `converged on N files` line). Record the results as testing.md rows; then L7 (docs) and S001's `done:` stamp.
  3. **S016** — move it to `plan_current/`, reproduce first (recipe in the step file's `excludes`), then fix red-first.
  4. Commit on `linux`; Max merges.
- **Deep-fuzz command in `CLAUDE.md` is red at HEAD** — seeds 1, 28 and 45 (measured 2026-11-11 at `06df839` and with S010 alike), default crash probability, all the §4.5 wedge — F09 / S002.
- **Last done:** S001's agent-reachable items — **L1, L2, L5, L3, L4**, one commit each (`afc7664`, `0d7d8f1`, `6ab38a6`, `e3bc8fe`, `229f497`), `make test` green at every one and the Linux arm checked with `GOOS=linux go build/vet`. L6 landed earlier (2026-07-25). Before that, the last closed work was W6 (2026-08-23) — see [plan_history.md](plan_history.md) and [testing.md](testing.md).
- **Also in `plan_current/`, waiting on Max's hardware (not counted as the agent's active step, DEC-002):** **S001 — finish the Linux port.** Still in `plan_current/`, no `done:` stamp. What landed: the `init` machine-name prompt (L1), the owner-only Linux daemon log (L2), the systemd unit corrections and the no-linger decision (L5), the per-platform `needsRebuild` (L3), and the inotify-limit diagnosis plus overflow-as-a-wake (L4). Rows W7-L1..L5 are in `testing.md`.
- **S001 next: L8, and it needs Linux hardware — Max's to run.** On `pop-os`, against the production inotify backend:
  - `go build ./... && go vet ./... && go test ./...` (was green 2026-07-25) plus `go test -race ./...`
  - `SYNCKEEPER_SCALE_FILES=50000 go test ./internal/watch/ -run 'TestScale|TestFSEventsScale' -timeout 20m` — expect ≈0 unwatchable directories
  - the 2 h soak (spec §16.9), **detached and with suspend inhibited**: `systemd-inhibit --what=sleep` — the Linux analogue of the idle-sleep lesson that killed three macOS soak attempts (decisions.md 2026-07-24)
  - **Then L7 (docs), and only then.** Doc claims are claims about *validated* code, so MANUAL/README platform wording is deliberately untouched until L8 passes. L7 also owes a MANUAL §9 line on mixed case-sensitivity (an ext4 machine can create `Readme.txt` + `README.txt` in one folder; APFS collapses them — first by file id wins, spec §5), and must reconcile MANUAL §9's existing *"macOS and Linux are validated on hardware"*, which today rests on W6's field report rather than on these gates.
- **Blocked:** none. S001's remainder is not blocked, it is Max-performed.
- **Parked:**
  - **L2b — log rotation — is flagged and unowned.** Named in S001's `excludes:` as "not yet a decision". Now more real than before: L2 took the Linux log out of journald, so journald's rotation no longer applies and growth is unbounded on **both** platforms (the launchd log always was). MANUAL states the fact; no policy invented. Proposal on the table: rotate at ~10 MB keeping one previous file, checked at daemon start and per cycle.
  - **Two defects found 2026-09-03 are planned but not authorized.** S004 (stored error details keep the URL and lose the cause) and S005 ("N local changes" counts wake-ups, not files) came out of reading Max's dashboard; both are diagnosed with evidence, neither is approved for a fix. Max reorders or authorizes.
  - **Every cycle that writes files fires its own watch events**, re-triggering the loop — sized under W3.3 as a bounded, self-terminating one-extra-cycle rescan, never a loop (measured ~19 s at 50k files, sub-second at personal scale). Suppression is an optimization, deferred. It is the benign half of what S005 makes legible.
  - **Deferred to publication:** the donation note (W2.4d), which lands in MANUAL.md §5 since the README/MANUAL consolidation. The repo is still private.
  - **Unscheduled, on the table from the W1.8/W1.9 close-outs:** the gate's **directory arm** — case-only dir renames, dir fold-adopts, R7-for-dirs, tracked case-only renames; one family, see decisions.md "W1.9.1"/"W1.8.8". W4's convergence oracle deliberately excludes it, so it will not surface there until built.
  - **Windows watch-rebuild answer is a guess, deliberately.** L3 left Windows on the conservative `needsRebuild = true` arm. `ReadDirectoryChangesW` holds one handle per directory and probably belongs with Linux, but measuring it is S006's job.
  - **Pre-existing, untouched:** `GOOS=windows go vet ./...` fails on `internal/watch/scale_test.go:65` (`undefined: syscall.Rlimit`). Test file only; `GOOS=windows go build ./...` is clean. Belongs to S006.
  - **Resolved, kept so it is not re-asked:** rotating the OAuth client before publication is *not* required — Max runs on debug credentials and accepts them as safe to publish (W5.5-S1, decisions.md 2026-07-24).
  - **AGENTS rule vs practice:** the recorded rule is *"subagents only for reviews and read-only exploration"*. Max overrode it verbally on 2026-09-03 and again on 2026-11-11 (DEC-002, scoped to the audit fix campaign); the rule text is **not** edited. If the override is meant to stand generally, it needs `/moltke:rules`.
