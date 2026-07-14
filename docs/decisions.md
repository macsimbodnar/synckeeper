# Synckeeper — Decision Log

Dated record of scope changes, spec deviations, and non-obvious technical choices. Newest first. Add an entry *before or alongside* the change it describes.

Format:

```
## YYYY-MM-DD — Short title
**Context:** why the question came up.
**Decision:** what was chosen.
**Consequences:** what it affects; link the spec/plan section if it amends one.
```

---

## 2026-07-14 — Phase 4 built: `init --adopt` is empty-baseline first-merge

**Context:** a second/third machine must join an existing non-empty Drive folder without a separate, risky merge planner.

**Decision:** adoption is **not** a new planner mode — it is the ordinary first sync run over an empty baseline, gated behind `--adopt`. With no `items` rows, the existing reconcile decision table's base-absent rows already produce exactly the required behavior: local-only → upload, remote-only → download, same-path same-md5 → adopt/record, same-path diff-md5 → conflict copy (remote wins canonical). Crucially, **an empty baseline can't generate a delete-class action** (deletes require a baseline row that's now missing on one side), so "nothing is deleted during adopt" is structural, not a special case. `init --adopt` therefore just: resolves the folder, and if it already holds files runs one `engine.Sync`.

**Gate:** plain `init` on a Drive folder that already contains files errors and points at `--adopt`, and persists nothing on that path so the `--adopt` retry is clean. An existing *empty* folder is still reused without `--adopt` (nothing to merge, no risk). `machine_id` was already stored by `initialize` from earlier work, so machine identity needed no new code.

**Consequences:** no new merge code to get wrong; the adopt path shares the exact reconcile/executor already validated by phases 1–3. `initialize` gained an `adopt bool` and now returns the folder id. Matrix tests (`internal/engine/adopt_test.go`): union merge, divergent-conflict, three-machine convergence with no lost version, adopt-while-others-active; plus the gate test in `cmd`. The remaining exit item — a real multi-machine rollout — needs physical machines and is the user's step.

## 2026-07-14 — Phase 6 Stage 2 built: control socket

**Context:** the "interact with the daemon" half of phase 6 — push commands into the running `watch` process.

**Decisions made during the build:**
- **One transport for all OSes: `net.Listen("unix", …)`.** Go supports AF_UNIX on Windows 10+, so a filesystem socket is a single code path (no build tags, no named-pipe dependency). 0600 on POSIX; stale-socket file removed on start, unlinked on shutdown. Windows AF_UNIX is compile-verified only (no Windows box here).
- **The control socket is a convenience, never a dependency.** If `Listen` fails (e.g. the config-dir path exceeds the ~104-char `sun_path` limit), the daemon logs a warning and runs on without control — syncing is never blocked. Same graceful-degradation posture as the fsnotify latch.
- **Commands that mutate loop state go through channels, not shared locks.** `sync`/`reload` hand work to the single-threaded sync loop (`syncNow`, `reloadCh`), keeping the loop the sole owner of the engine, ticker, and config — no locking around `Eng.Cfg`. `pause` is a mutex-guarded flag on the recorder that the loop checks. Verified race-clean.
- **Monitoring stays DB-sourced; the socket is control-only (plus `ping`).** `status`/`activity` keep reading the DB (fresh within 10 s, and they work when the daemon is down); `status` only *adds* a socket `ping` as an authoritative liveness signal that overrides heartbeat staleness. This avoids duplicating the render over the socket and keeps read commands functional with no daemon.
- **`sync --wait` is client-side.** A delegated `sync` triggers the daemon then the CLI polls the recorded status for completion (last-sync advance or cycle-summary change), rather than the server holding the connection until the cycle ends. Simpler protocol; 1-second `last_sync_at` granularity is covered by also comparing the cycle summary. `--dry-run` isn't delegated (it needs a standalone run without the lock) — the CLI says so and points at stopping the daemon.
- **`reload` hot vs cold fields:** poll interval, ignore globs, mass-delete threshold, quarantine retention apply live; sync_dir, folder_name, machine_name are identity/path and reported as needing a restart (left untouched so the report matches reality).
- **Protocol version handshake** (`control.ProtocolVersion`) on every request/response so a stale CLI against a newer daemon (or vice-versa) fails with "rebuild so both match" instead of misparsing.

## 2026-07-14 — Phase 6 Stage 1 built: monitoring via a DB heartbeat

**Context:** implementing the read-only half of [phase-6.md](phase-6.md) — see status/activity of the running daemon without any IPC.

**Decisions made during the build:**
- **Activity is per-action, derived from the successful cycle's plan** (upload/download/trash/move/mkdir/conflict; Record/Forget skipped), not the cycle-level summary the design floated. This yields the Dropbox-style recent-activity list the user asked for and costs nothing extra: the watch loop already has `Result.Plan`, so the executor stays untouched. A *failed* cycle records a single error entry instead — we can't tell which planned actions actually ran, so we never claim per-action success.
- **Heartbeat is a separate goroutine** (10 s, `watch.HeartbeatInterval`), not folded into the main select loop, so a long-running sync cycle can't make a live daemon look stale. Staleness window = 3× the interval. Liveness with no socket is: `Running` flag AND heartbeat within the window → running; `Running` but stale → "likely crashed"; not `Running` → "stopped".
- **Read commands never take the instance lock** (`readEnv`, distinct from `appEnv`): they read via SQLite WAL alongside the daemon's single writer. This is what lets `status` run while `watch` holds the lock.
- **`paused` mode deferred to Stage 2**: pausing is only meaningful if something can toggle it, which is the control socket.
- **Account email not fetched**: `account` shows token presence/expiry/refresh only; the Google email needs an `about.get` API call (network + a Client-interface addition). Deferred as a follow-up rather than pull a network dependency into a monitoring command that otherwise works offline.
- **Guard-blocked surfacing** needed a typed error: added `guards.ErrMassDelete` (wrapped) so the recorder can distinguish a mass-delete block (actionable: `--confirm-deletes`) from any other cycle error and set `guard_blocked` in status.

## 2026-07-12 — Phase 6 added: daemon control & monitoring

**Context:** the daemon is headless — `status` reads config/DB files but never talks to the running `watch` process, so there is no way to see if it is alive, what it is doing, or to drive it (sync now, pause). The user wants monitoring and interaction, console-first, with a possible tray/menu-bar icon later (à la Dropbox). This is scope beyond the spec's phases 0–5.

**Decision:** add a phase 6 ([phase-6.md](phase-6.md)), designed now and built later (after 4–5). Key choices:

- **Monitor and control are split.** Monitoring (running?/mode/last sync/activity/config/account/autostart) needs *no* IPC: the daemon records a heartbeat + activity ring to its SQLite DB (new migration v3) and the CLI reads it — this also works when the daemon is down. Control (sync now/pause/resume/reload) needs a channel *into* the process because the daemon holds the flock instance lock, so a CLI can't run its own sync; "sync now" is *delegated*.
- **The daemon's one interface is a local control socket** — Unix-domain socket / AF_UNIX (0600), never a TCP port (filesystem perms are the auth; no network surface). Line-delimited JSON with a version handshake. The CLI and the future GUI are both just clients of it.
- **The tray GUI is a separate, optional, per-platform binary that may use cgo**, excluded from `make build-all`. Rationale: a macOS menu-bar icon (NSStatusBar) requires Cocoa → cgo, which is fundamentally incompatible with the project's hard `CGO_ENABLED=0` / cross-compile-from-one-machine guarantee. Quarantining the GUI (and its cgo) behind the socket keeps the sync core pure-Go and static, and the GUI holds no sync logic so it can't threaten data integrity.

**Consequences:** three build stages (monitoring / control socket / tray), each independently shippable. statedb gains a v3 migration. Read-only CLI commands must stay lock-free (like today's `status`) so they don't contend the daemon's lock. Open questions (persist pause across restart; activity granularity; line-JSON vs HTTP-over-socket; Windows AF_UNIX vs named pipe; tray toolkit) are recorded in phase-6.md, not yet decided.

## 2026-07-12 — Post-soak fixes: remote-cache pruning and a polling-only latch

**Context:** the full 2-hour soak passed (converged on 10,858 files) but surfaced two long-run resource problems. First, `remote_nodes` grew monotonically: the changes feed is drive-wide, trashed files were cached as tombstone rows forever, and children of a trashed folder became permanently unreachable garbage — all loaded into memory every cycle by `AllRemoteNodes`. Second, when the tree outgrew the fd limit (~10k files per watcher on kqueue), watch re-registration failed and logged an error every cycle, forever.

**Decision (cache):** three coordinated changes in `remotedelta`. (1) A trashed change *deletes* the cache row instead of upserting a tombstone. (2) After each changes batch, a prune pass deletes every row unreachable from the root folder (orphans of deleted folders, out-of-tree files, legacy tombstones). (3) To keep "folder moved/restored into the tree" correct now that out-of-tree rows are pruned, a change for a *folder we have never cached* whose parent is in-tree triggers a one-time subtree walk (`files.list` BFS) before the page token is persisted. Drive emits no change events for the descendants of a moved folder, so without the walk a pruned subtree would sync in empty — this also fixes the pre-existing gap for folders created before `init` and for restore-from-trash of a folder.

**Decision (watch):** three consecutive cycles with watch-registration failures latch the daemon into polling-only mode: the fsnotify watcher is closed outright (releasing every fd, so the scanner stops starving), one warning is logged, and file watching is retried at the existing rebuild cadence (every 500 cycles), restoring event-driven sync automatically if the pressure clears.

**Consequences:** the cache now tracks only the synced tree, so per-cycle memory is proportional to tree size, not Drive history; out-of-tree folder creations cost one extra `files.list` each before being pruned. A latched daemon has poll-interval latency instead of sub-second reactivity — correct by design, since polling full-syncs everything. Regression tests in `internal/remotedelta` (trash-drop, orphan prune, out-of-tree prune, move-in walk) and `internal/watch` (latch counter); 90 s soak re-run green.

## 2026-07-08 — Soak-discovered bug: fsnotify fd exhaustion on macOS

**Context:** the first 2-hour soak died after ~1 h with `too many open files`; the daemon degraded gracefully (logged, backed off) but could not recover because fsnotify held the descriptors.

**Cause:** fsnotify's kqueue backend (macOS/BSD) opens one fd per watched *file*, not just per directory — and those fds leak outright when files are deleted faster than their delete events are processed (an unlinked file's fd never returns).

**Decision:** two mitigations in `watch`: (1) raise `RLIMIT_NOFILE` soft → hard at startup (darwin fallback to OPEN_MAX); (2) rebuild the fsnotify watcher every 500 sync cycles — `Close()` releases every fd it holds and the full-scan poll covers the swap window. Watch-registration failures degrade to polling with an aggregate warning instead of failing.

**Consequences:** the macOS fd cost still scales with tree size (~1 fd per file while watched); fine to ~tens of thousands of files. Linux (inotify) and Windows (ReadDirectoryChangesW) don't have the per-file cost. This is exactly the class of bug the soak exists to catch — keep the soak in the release ritual.

## 2026-07-08 — Phase 3 implementation decisions

**Context:** continuous mode could have grown a second, incremental sync path; it deliberately didn't.

- **Every watch trigger runs a full `engine.Sync`** (full local scan + changes poll) instead of the spec's targeted subtree rescans. At personal scale a full scan costs milliseconds, and one code path means watch mode can't behave differently from manual sync. Consequences: the spec's hourly full rescan is subsumed (every cycle is one), and dropped fsnotify events are recovered within one poll interval. Revisit only if the tree grows past ~100k files.
- **The watcher syncs once at startup**, catching changes made while the daemon was down — no event will ever fire for those.
- **Guard errors do not kill the daemon.** A tripped mass-delete or empty-dir guard logs loudly and keeps retrying with backoff; the human resolves it (e.g. `sync --confirm-deletes`) while watch keeps the machine otherwise in sync. The daemon never self-confirms deletions.
- **Soak is time-gated by `SYNCKEEPER_SOAK_SECONDS`** (7200 = the 2-hour exit gate) so the normal test suite stays fast; soak machines run with mass_delete_threshold=1.0 because chaos deletes freely and the guard is aimed at humans.

## 2026-07-08 — Phase 2 implementation decisions

**Context:** hardening surfaced how to simulate crashes and what repair may touch.

- **Fault injection aborts ops via error, not panic/exit.** A panic inside a transfer-pool goroutine would kill the test binary. Error-abort leaves the identical on-disk/remote/DB state as a crash at that checkpoint, except deferred temp cleanup — covered separately by planting orphan temps. A genuine `kill -9` mid-upload was run manually against real Drive and repaired cleanly.
- **`doctor --repair` only ever adds.** It restores meta (folder id by configured name, machine id, fresh page token), force-rebuilds the remote cache, and adopts md5-equal local/remote pairs into the baseline. It never trashes, quarantines, or overwrites: after a lost DB, one-sided files become plain uploads/downloads on the next sync, and deletions are structurally impossible (baseline ⊆ matched pairs).
- **Quarantine purge runs only after a fully successful sync** and only removes synckeeper's own dated folders (`YYYY-MM-DD`); anything else in the quarantine dir is left alone.
- **`cmd` grew a shared `appEnv` helper** (lock + config + DB + lazy Drive client) used by `sync` and `doctor`; `init` keeps its special flow.

## 2026-07-08 — Phase 1 implementation decisions

**Context:** building the sync engine surfaced choices the spec left open.

- **`internal/engine` package added** (not in the spec's repo layout): orchestrates one sync cycle (guards → remote delta → scan → reconcile → execute) so `sync`, phase-3 `watch`, and the scenario tests share one pipeline. `executor` stays limited to applying plans, per its spec role.
- **`remote_nodes` cache table (schema v2) stores all Drive changes, not just in-tree ones.** `changes.list` has no folder filter; tree membership is decided at snapshot time by walking parent links from the root folder. Costs a few rows per Drive file; personal-scale fine. Out-of-tree moves fall out naturally (parent chain breaks → remote delete).
- **Crash resume is discard-and-replan, not op resumption.** Stale pending_ops are dropped and orphan temps deleted at sync start; the fresh plan self-heals partial effects (an uploaded-but-uncommitted file reappears as both-new-same-md5 → adopt). pending_ops still journals every action, so phase 2's fault tests can verify the model and add targeted repair if F1–F3 reveal gaps.
- **Subtree deletes trash each file/folder individually, bottom-up** rather than trashing the top folder in one call: uniform op model and per-item journaling, at the cost of more Drive-trash entries. Restore-from-trash is a manual rescue path anyway.
- **Guards G1/G2 landed in phase 1** (spec put them in phase 2): once deletes propagate, running without the mass-delete and empty-dir guards was too risky even for daily personal use.
- **Deleting the last tracked file requires a non-empty dir**: G2 counts all directory entries, so an empty-dir-after-legitimate-delete is indistinguishable from an unmount and errors. Workaround: any other file (even an ignored one) in the dir. Revisit in phase 2 if it annoys.
- **Real Drive bumps a file's version shortly after upload** (post-processing), so the sync after an upload does one `record` row refresh with no transfer. Benign and self-limiting; noted so nobody chases it as a bug.

## 2026-07-06 — Baseline decisions inherited from the spec

Recorded here so future deviations have something to deviate from:

- Pure-Go only, `CGO_ENABLED=0`: cross-compilation from one machine is a hard requirement; any dependency pulling cgo is rejected.
- `modernc.org/sqlite` over mattn/go-sqlite3: the latter is cgo.
- Polling over push notifications: webhooks need a public HTTPS endpoint; unacceptable for a personal tool.
- Quarantine dir over OS-native trash: cross-platform Go trash libraries are immature; quarantine is simpler and equally safe.
- OAuth consent screen published to Production but unverified: Testing status expires refresh tokens after 7 days, which breaks a daemon.
- `reconcile` is a pure function: the correctness core must be exhaustively unit-testable with no I/O.
- Edit beats delete, always: a resurrected file is a nuisance; a lost edit is data loss.
- Remote wins the canonical name in conflicts: deterministic across machines (all machines agree on what Drive holds), and the local version is preserved as the conflicted copy — nothing is lost either way.
