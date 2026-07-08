# Phase 3 — Continuous mode

**Goal:** `synckeeper watch` runs as a daemon: local fsnotify events + remote polling feed the same phase-1 reconcile engine. Service wrappers per OS.
**Exit criterion:** two-hour soak with random edits on both sides, no divergence.
**Status:** in progress — code complete 2026-07-08, 60 s soak green, full 2-hour soak running

## Tasks

### Watch manager (`internal/watch`)
- [x] fsnotify per-directory watches; manager adds watches for new dirs immediately on their create event (files may land before the post-sync refresh) and re-walks after each sync.
- [x] Debounce/coalesce events (500 ms quiet window) into sync triggers.
- [x] Targeted rescan: NOT implemented — every trigger runs a full engine.Sync (full local scan + changes poll). Deviation: at personal scale a full scan is milliseconds; one code path beats two. Revisit only if the tree grows huge.
- [x] Remote polling loop every `poll_interval_secs`: the ticker triggers the same sync loop.
- [x] Periodic full scan: inherent — every cycle IS a full scan, so dropped fsnotify events are caught within one poll interval (stronger than the hourly requirement).
- [x] Single reconcile/execute pipeline shared with `sync` — no second code path; one serialized sync loop (triggers coalesce into a 1-buffered channel).
- [x] Backoff when offline: failed cycles retry with exponential backoff up to 10× poll interval, reset on success. Guard errors keep the daemon alive and waiting for the human.
- [x] Clean shutdown on SIGINT/SIGTERM: context cancellation; in-flight ops stay journaled and replan next run; lock released.

### `synckeeper watch` command
- [x] Wires watch manager + poller + engine; logs each cycle summary at info level.
- [x] Refuses to start if another instance holds the lock.

### Service wrappers (`internal/service`)
- [x] `service install` generates + installs: launchd plist (macOS), systemd user unit (Linux), Task Scheduler XML (Windows); `service uninstall` reverses. Generators unit-tested; install/uninstall exercised manually per platform.
- [x] Wrappers run `synckeeper watch`, restart on failure, log to the platform-native place.

### Soak test
- [x] Scripted soak harness: two machines with live watchers + chaos goroutines against the fake; convergence + doctor checks at the end. Gated by SYNCKEEPER_SOAK_SECONDS.

## Verification

- [ ] Soak passes with no divergence.
- [ ] Dropped-event recovery proven: suppress events artificially, confirm hourly full scan converges.
- [ ] `service install` + reboot on the dev machine: watch comes back by itself.
