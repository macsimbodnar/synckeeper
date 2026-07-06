# Phase 3 — Continuous mode

**Goal:** `synckeeper watch` runs as a daemon: local fsnotify events + remote polling feed the same phase-1 reconcile engine. Service wrappers per OS.
**Exit criterion:** two-hour soak with random edits on both sides, no divergence.
**Status:** not started

## Tasks

### Watch manager (`internal/watch`)
- [ ] fsnotify per-directory watches; manager adds watches for new dirs and drops them for removed dirs (walk new dirs immediately — events may have been missed between mkdir and watch add).
- [ ] Debounce/coalesce events (e.g. 500 ms quiet window per path subtree) into targeted rescan requests.
- [ ] Targeted rescan in `scanner`: rescan only requested subtrees, merge into snapshot.
- [ ] Remote polling loop every `poll_interval_secs`: consume changes feed → targeted reconcile.
- [ ] Periodic full scan every `full_rescan_interval_secs` (default hourly) to catch dropped events.
- [ ] Single reconcile/execute pipeline shared with `sync` — no second code path; runs serialized (one reconcile at a time; new triggers queue).
- [ ] Backoff when offline: network errors pause polling with exponential backoff, resume cleanly.
- [ ] Clean shutdown on SIGINT/SIGTERM: finish or journal in-flight ops, release lock.

### `synckeeper watch` command
- [ ] Wires watch manager + poller + engine; logs each cycle summary at info level.
- [ ] Refuses to start if another instance holds the lock.

### Service wrappers (`internal/service`)
- [ ] `service install` generates + installs: systemd user unit (Linux), launchd plist (macOS), Task Scheduler XML (Windows); `service uninstall` reverses.
- [ ] Wrappers run `synckeeper watch`, restart on failure, log to the platform-native place.

### Soak test
- [ ] Scripted soak harness: two simulated machines (or machine + fake), random create/edit/rename/delete on both sides for 2 h; assert full convergence and zero lost content at the end.

## Verification

- [ ] Soak passes with no divergence.
- [ ] Dropped-event recovery proven: suppress events artificially, confirm hourly full scan converges.
- [ ] `service install` + reboot on the dev machine: watch comes back by itself.
