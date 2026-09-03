# Status

Convenience view, rewritten by hand at the end of any turn that changed plan
state. The filesystem beats this file: on disagreement, `plan_current/` wins
and this file is rewritten to match it.

Updated: 2026-09-03, by Claude (agent) at Max's direction

- **Last done:** S001's agent-reachable items — **L1, L2, L5, L3, L4**, one commit each (`afc7664`, `0d7d8f1`, `6ab38a6`, `e3bc8fe`, `229f497`), `make test` green at every one and the Linux arm checked with `GOOS=linux go build/vet`. L6 landed earlier (2026-07-25). Before that, the last closed work was W6 (2026-08-23) — see [plan_history.md](plan_history.md) and [testing.md](testing.md).
- **In progress:** **S001 — finish the Linux port.** Still in `plan_current/`, no `done:` stamp. What landed: the `init` machine-name prompt (L1), the owner-only Linux daemon log (L2), the systemd unit corrections and the no-linger decision (L5), the per-platform `needsRebuild` (L3), and the inotify-limit diagnosis plus overflow-as-a-wake (L4). Rows W7-L1..L5 are in `testing.md`.
- **Next: L8, and it needs Linux hardware — Max's to run.** On `pop-os`, against the production inotify backend:
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
  - **Session note:** the recorded AGENTS rule is *"subagents only for reviews and read-only exploration"*. Max overrode it verbally for this session (one implementing agent per L-item); the rule text was **not** edited and no decision was recorded. If that override is meant to stand, it needs `/moltke:rules` and a `DEC-` entry.
