# Status

Convenience view, rewritten by hand at the end of any turn that changed plan
state. The filesystem beats this file: on disagreement, `plan_current/` wins
and this file is rewritten to match it.

Updated: 2026-11-12 03:00, by Claude (agent) on `pop-os` at Max's direction

- **Audit 2026-11-11 (adversarial, commit `1cea1b7`): 11 findings, all `planned`.** Report: [audit/2026-11-11_adversarial.md](audit/2026-11-11_adversarial.md). Steps S007–S016, plus F09 onto S002. Eight gated repro tests (`SYNCKEEPER_AUDIT_REPRO=1`); each fix removes its test's gate. MANUAL §8 carries every finding as a known bug until its fix lands.
- **Fix campaign authorized by Max 2026-11-11 (DEC-002):** audit bugs first, in `plan.md` order; distribution (S012, the OAuth client question) deferred and uncertain. One Opus subagent implements each step, sequentially; the main session coordinates, reviews, and commits.
- **The R25 wedge needs no crash** (S002): both machines rename a folder the same way, one edits a file inside → the other wedges every cycle. Reproduced by S023's implementer; MANUAL §8 corrected. S002 is no longer a rare-crash defect — worth moving up among the steps that wait on Max.
- **In progress: S002** (the §4.5 rename wedge — planner fix + visible standstill, DEC-004).
- **Done 2026-11-11, one commit each, a fast check on each:** S007–S011, S013, S014, S018, S021, S023, S026. `make test` green at every commit. S010's, S011's and S021's real-client paths have never run against real Drive.
- **Max's decisions, 2026-11-11 (DEC-003..DEC-009):** S023 → new rule; S002 → planner + visible standstill; S017 → marker file in the sync folder; S020 → rename beats delete; S022 → partial upload stays failed, gates fixed; **S024 and S025 → not built, residuals accepted** (in `plan_done/`, stamped NOT BUILT; MANUAL §8 keeps both rows). Live-Drive tests authorized ("Do il permesso al test").
- **Linux session on `pop-os`, 2026-11-12 (Max started it; works on `master`, `git pull` before each commit, never push):**
  - **S016 done** (`1019cae`): F11 reproduced on hardware first and confirmed; the guard and the executor now probe the bin the sync dir really uses (`trash.AvailableFor`). Residuals: `status`/`doctor`/`info`/daemon start line still name the home bin (S028); a mount *inside* the sync dir is not probed (testing.md S016.6). MANUAL §8 row says both.
  - **S027 done** (Max: "Fix ora, p.Quit()"): the dashboard's cancellation goes through `p.Quit()`, not `tea.WithContext`; the race was 23 of 46 runs before, 0 of 60 after.
  - **S001 done — the Linux port is closed.** L8 on hardware, inotify: build/vet/test green; `-race` red at `5c1a807` → S027 → green at `43c8c89`; 50k scale 0 of 500 dirs unwatchable (756 s); **2 h soak converged on 16,419 files** (00:38→02:38, `1019cae`, detached under `systemd-inhibit --what=sleep`). Rows: testing.md "S001 — L8 …" L8.1–L8.5, L7. L7: MANUAL §9 validation bullet cites L8, new mixed case-sensitivity bullet; README status line. specs §16.9 and the Roadmap record the pass.
  - **Deviations from the handoff's commands:** the soak ran with `XDG_DATA_HOME` in a scratch dir — **16,455 chaos files (131 MB) would otherwise have landed in `~/.local/share/Trash`** (S029, now measured); `-count=1` on every gate; scale with `-v`; logs in the session scratchpad, not repo `soak.log`; F11 reproduced in a user namespace, not with `sudo` (no password in the session).
- **Next by plan order:** S020, S017, S022 (agents, Mac); S019 and S015 (live Drive, Mac); S029, S028 — their positions are the agent's proposal, Max reorders. Nothing left that needs `pop-os` except S028 (Linux-only display) and S029's Linux arm.
- **Deep-fuzz command in `CLAUDE.md` is red at HEAD** — seeds 1, 28 and 45 (measured 2026-11-11 at `06df839` and with S010 alike), default crash probability, all the §4.5 wedge — F09 / S002.
- **Last done:** S001, S027, S016 (2026-11-12, `pop-os`). Before them S007–S011, S013, S014, S018, S021, S023, S026 (2026-11-11).
- **In `plan_current/`:** S002 (Mac session, in progress).
- **Blocked:** none.
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
