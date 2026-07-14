# Synckeeper — Execution Plan

Master tracking document. The spec in [spec.md](spec.md) is the contract; this file tracks how and in what order it gets built. Each phase has a detailed task file; check off tasks there and update the status table here.

## Status

| Phase | Doc | Goal | Exit criterion | Status |
|---|---|---|---|---|
| 0 | [phase-0.md](phase-0.md) | Skeleton: auth + plumbing | Authenticate and list the Drive folder; all four binaries build | done 2026-07-07 |
| 1 | [phase-1.md](phase-1.md) | One-shot bidirectional sync | Scenario tests S1–S8 pass | done 2026-07-08 |
| 2 | [phase-2.md](phase-2.md) | Safety hardening | Fault tests F1–F5 pass | done 2026-07-08 |
| 3 | [phase-3.md](phase-3.md) | Continuous mode (`watch` + services) | 2-hour soak with random edits on both sides, no divergence | done 2026-07-09 |
| 4 | [phase-4.md](phase-4.md) | Multi-machine rollout (`init --adopt`) | 3 machines, offline concurrent edit matrix passes | code + matrix tests done 2026-07-14; manual rollout pending |
| 5 | [phase-5.md](phase-5.md) | Cross-platform hardening | Full test suite green on Linux, macOS, Windows | **deferred to last** (other architectures) — 2026-07-14 replan |
| 6 | [phase-6.md](phase-6.md) | Daemon control & monitoring | Live `status`/`sync`/`pause` reach the running daemon; `service status` reports autostart | Stages 1–2 done 2026-07-14; tray GUI (3) → folded into phase 7 |
| 7 | [phase-7.md](phase-7.md) | macOS experience | Native-feeling status & control on macOS (quick wins, tray, Finder badges) | in progress — quick wins first (2026-07-14) |

## Revised roadmap (2026-07-14) — macOS-first

The tool is correct and daily-usable on macOS (phases 0–4, 6.1–6.2). Remaining work is reprioritized to polish the **macOS** experience first and defer everything specific to **other architectures** (Windows/Linux) to the end. Ordering now:

1. **Near-term — macOS experience ([phase 7](phase-7.md)):** quick wins (activity remote/local labeling, add folder to Finder sidebar, case-collision safety — APFS is case-insensitive, so this matters here), then the macOS menu-bar tray icon (phase 6 stage 3), then Finder sync badges.
2. **Middle — needs a second Mac (same architecture):** phase 4 real multi-machine rollout; phase 3 `service install` + reboot check.
3. **Last — other architectures ([phase 5](phase-5.md)):** Windows name hardening (reserved names, illegal chars, long paths, NTFS atomicity), cross-platform tray variants (Linux DBus, Windows Shell_NotifyIcon), and the full suite run on real Linux + Windows.

Note: the case-collision item lives in phase 7 (it affects macOS/APFS) even though phase-5.md still lists it; the rest of phase 5 is the deferred Windows/Linux work.

Statuses: `not started` → `in progress` → `blocked (reason)` → `done (date)`.

## Working process

1. Work phases strictly in order; a phase starts only when the previous phase's exit criterion is met and recorded here.
2. Within a phase, tasks are listed in dependency order in the phase doc. Check off tasks (`- [x]`) as they complete; note deviations inline under the task.
3. Every scope change, spec deviation, or non-obvious technical choice gets a dated entry in [decisions.md](decisions.md) before or alongside the code change.
4. Tests are written with the feature, not after. The test matrix in [testing.md](testing.md) tracks which scenario/fault/guard tests exist and pass.
5. The [README](../README.md) build/run/test instructions must stay correct at every commit — if a Makefile target or command changes, update the README in the same change.
6. Phase 1 is the "usable daily" milestone: after it, real files can be trusted to the tool via manual `sync` runs.

## Build order rationale

- **Phase 0 before everything**: auth, config, DB, and the Drive client interface are dependencies of every other package. Getting `driveclient` defined as an interface early is what makes the whole test strategy (in-memory fake) possible.
- **`reconcile` is built pure and test-first in phase 1**: it is the correctness core. The decision table in the spec maps 1:1 to table-driven test cases; write the table tests before wiring the executor.
- **Guards land in phase 2, but stubs exist from phase 1**: `sync` calls the guard hooks from day one so phase 2 only fills in logic, not plumbing.
- **`watch` (phase 3) reuses the phase 1 engine unchanged**: fsnotify events and remote polling only decide *when* to run a targeted reconcile; they must not introduce a second sync code path.
- **Multi-machine (phase 4) needs only `--adopt`**: the reconcile engine is already machine-agnostic; adoption is a special first-merge planner mode.
- **Platform quirks last (phase 5)**: `internal/names` exists from phase 1 with the mapping logic; phase 5 fills in the Windows/macOS edge cases and runs the suite on real machines.
- **Control & monitoring (phase 6) is additive and sits on the seam**: the daemon stays headless and pure-Go; monitoring is a DB heartbeat the CLI reads, control is a local socket the CLI (and a later, separate, cgo-allowed tray binary) drives. Deferred to last because it touches no durability invariant — it observes and triggers the phase-1 engine, never a second sync path.

## Key risks to watch

| Risk | Mitigation |
|---|---|
| Drive `changes.list` semantics (out-of-tree moves, trashed parents) subtler than expected | `remotedelta` keeps an in-DB parent map; live smoke tests (`SYNCKEEPER_LIVE_TEST=1`) validate the fake against real Drive behavior early in phase 1 |
| Windows rename-with-replace not truly atomic | pending_ops journal brackets the remove+rename; F3 fault test covers the gap window |
| fsnotify event loss / platform differences | hourly full rescan is the safety net; soak test is the exit gate |
| mtime granularity differences across filesystems | store mtime_ns but compare with tolerance; md5 is the tiebreaker |
| OAuth consent screen left in Testing → tokens die in 7 days | init doc + README call out Production/unverified requirement explicitly |
