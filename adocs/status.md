# Status

Convenience view, rewritten by hand at the end of any turn that changed plan
state. The filesystem beats this file: on disagreement, `plan_current/` wins
and this file is rewritten to match it.

Updated: 2026-09-03, by Claude (agent) at Max's direction

- **Last done:** nothing under this structure. The last work finished before adoption was W6, closed 2026-08-23 on Max's field report (*"it is rolled out on mac and linux since a month now and works fine"*), which satisfied specs §16.10 — the one acceptance criterion the offline suite structurally cannot prove. The full record is [plan_history.md](plan_history.md) (W1–W21) and [testing.md](testing.md).
- **In progress:** none. `plan_current/` is empty.
- **Next:** S001 — finish the Linux port. L6 already landed 2026-07-25; L1–L5, L7 and the L8 hardware gates remain. Inside S001 the order is L1 → L2 → L5 → L3 → L4 → L8 (gates) → L7 (docs): L1/L2/L5 change what `init` asks and what `service install` writes, so they land before the service is reinstalled, and docs come last because doc claims are claims about *validated* code.
- **Blocked:** none.
- **Parked:**
  - **Two defects found 2026-09-03 are planned but not authorized.** S004 (stored error details keep the URL and lose the cause) and S005 ("N local changes" counts wake-ups, not files) came out of reading Max's dashboard; both are diagnosed with evidence, neither is approved for a fix. Max reorders or authorizes.
  - **Every cycle that writes files fires its own watch events**, re-triggering the loop — sized under W3.3 as a bounded, self-terminating one-extra-cycle rescan, never a loop (measured ~19 s at 50k files, sub-second at personal scale). Suppression is an optimization, deferred. It is the benign half of what S005 makes legible.
  - **Deferred to publication:** the donation note (W2.4d), which lands in MANUAL.md §5 since the README/MANUAL consolidation. The repo is still private.
  - **Unscheduled, on the table from the W1.8/W1.9 close-outs:** the gate's **directory arm** — case-only dir renames, dir fold-adopts, R7-for-dirs, tracked case-only renames; one family, see decisions.md "W1.9.1"/"W1.8.8". W4's convergence oracle deliberately excludes it, so it will not surface there until built.
  - **Resolved, kept so it is not re-asked:** rotating the OAuth client before publication is *not* required — Max runs on debug credentials and accepts them as safe to publish (W5.5-S1, decisions.md 2026-07-24).
