# Phase 6 — Daemon control & monitoring

**Goal:** a way to observe and drive the running `watch` daemon — see whether it is alive, what it is doing, its config and account, recent activity, and autostart state; and send it commands (sync now, pause/resume). Console-first, with a later optional tray/menu-bar GUI reusing the same interface.
**Exit criterion:** from a second terminal while `watch` runs, `synckeeper status` reports live daemon state (mode, last sync, recent activity, guard-blocked); `synckeeper sync`/`pause`/`resume` reach the running daemon; `service status` reports autostart state. All pure-Go, `CGO_ENABLED=0`, cross-compiled.
**Status:** in progress — Stage 1 (monitoring) and Stage 2 (control socket) done 2026-07-14; Stage 3 (tray GUI) not started. Built ahead of phases 4–5 at the user's request because it touches no durability invariant.

> Scope note: this is an addition beyond the original spec's phases 0–5. See decisions.md (2026-07-12, "Phase 6 added: daemon control & monitoring").

---

## The core distinction: monitor vs. control

Two needs with very different costs, kept separate on purpose:

- **Monitor (read):** running? / mode / last sync / recent activity / config / sync dir / account / autostart on-off. **Needs no live channel into the daemon.** Config, sync dir, and account are files. Autostart state is one `launchctl`/`systemctl`/`schtasks` query. The only live facts — last-sync time, last cycle result, current mode, recent actions — just need the daemon to *record what it did* to the SQLite DB it already holds open; the CLI reads those rows. This still works when the daemon is **down** (it shows the last recorded state plus "last seen Nm ago").
- **Control (commands):** sync now / pause / resume / reload. **Needs a channel into the running process.** This is the only reason to add IPC. Note *why* the CLI can't just run a sync itself while the daemon runs: the daemon holds the flock instance lock, so a second syncer cannot start (`openAppEnv` would fail to acquire the lock). "Sync now" must therefore be *delegated* to the daemon over the control socket.

Design consequence: monitoring is built with **zero IPC** (DB heartbeat); control is built on a **local socket**. They ship as separate stages so the read-only value lands first and at near-zero risk.

## Architecture

Keep the daemon **headless and pure-Go**. Its one machine interface is a **local control socket**. The CLI subcommands are thin clients of that socket (for control) and of the DB/config files (for monitoring). The future tray GUI is **another client of the same socket** — no new daemon code.

```
              ┌────────────────────────── watch daemon (pure Go, CGO=0) ─────────┐
              │  sync loop  ──writes──▶  state.db: daemon_status + activity        │
              │  control listener  ◀── unix socket / named pipe (0600, no network) │
              └───────────▲───────────────────────────▲──────────────────────────┘
                          │ socket (control)           │ read-only (monitor)
        ┌─────────────────┴───────┐         ┌──────────┴───────────────┐
        │ synckeeper CLI (pure Go) │         │ synckeeper CLI: status,   │
        │  sync/pause/resume/reload│         │  activity, config, account│
        └──────────────────────────┘         │  (reads DB + files)       │
                                             └───────────────────────────┘
        ┌──────────────────────────────────────────────┐
        │ tray/menu-bar GUI  (SEPARATE binary, cgo OK)  │  ── same socket ──▶
        │  built per-OS, NOT in make build-all           │
        └────────────────────────────────────────────────┘
```

### The CGO / tray constraint (decided up front)

A macOS menu-bar icon (NSStatusBar) requires calling into Cocoa → **cgo**, and must be built on/for macOS with the SDK. That is fundamentally incompatible with the project's hard `CGO_ENABLED=0`, single-static-binary, cross-compile-from-one-machine requirement. Linux (DBus StatusNotifierItem) and Windows (Shell_NotifyIcon) can stay pure-Go, but macOS cannot.

**Decision:** the GUI is a **separate, optional, per-platform binary** (`cmd/synckeeper-tray`, or its own repo) that is allowed to use cgo and is built on each OS. It is **not** part of `make build-all` and cannot weaken the core binary's guarantee. It contains no sync logic — it only calls the control socket — so the cgo/UI risk cannot touch data integrity. Designing for this now (socket as the seam) avoids discovering it at the tray stage.

## Control socket

- **Transport:** Unix-domain socket at `<config_dir>/control.sock`, mode **0600** on macOS/Linux. On Windows, prefer Go's AF_UNIX support (Windows 10+, a socket file, zero new deps); fall back to a named pipe via `github.com/Microsoft/go-winio` (pure Go) if AF_UNIX proves flaky. **Never a TCP port** — filesystem permissions are the auth; a localhost port would be reachable by any local process/user.
- **Lifecycle:** the daemon removes a stale socket file on startup, creates the listener, and unlinks it on clean shutdown. Socket present + a successful `ping` = authoritative liveness; the DB heartbeat is the fallback that also works with no socket / after a crash.
- **Framing:** line-delimited JSON — client writes one request object + `\n`, daemon writes one response object + `\n`, one request per connection in v1. (Streaming `subscribe` for the tray's live updates is added in the GUI stage as a long-lived connection emitting newline-delimited status objects.)
- **Protocol versioning:** every request/response carries a `v` field; mismatched CLI/daemon degrade gracefully with a clear "restart the daemon to match this binary" message rather than misparsing.
- **Alternative considered:** HTTP over the unix socket (`net/http` + unix listener) — nicer routing, curl-debuggable, and a straight path to a future web UI, at the cost of running an HTTP server inside the daemon. Deferred; revisit only if a browser UI is wanted. Line-delimited JSON is the v1 default.

### Requests (client → daemon)

| cmd | args | response | notes |
|---|---|---|---|
| `ping` | — | `{v, ok}` | liveness + version handshake |
| `status` | — | full status snapshot (below) | same object the tray consumes |
| `activity` | `{limit}` | `[{ts, kind, rel_path, detail}]` | most recent first |
| `sync` | `{wait}` | `{triggered}` or, if `wait`, the cycle `Result` | delegates to the daemon's trigger channel; guards still apply |
| `pause` | — | `{paused:true}` | daemon keeps running, skips triggers/ticks until resumed; still answers control |
| `resume` | — | `{paused:false}` | |
| `reload` | — | `{reloaded, needs_restart:[...]}` | re-read config.toml; hot-swap poll interval / ignore / threshold; report fields that need a restart |

Status snapshot (JSON; the human `status` renders this):

```
{ v, running, pid, started_at, uptime,
  mode,                 // watching | polling-only | backoff | paused
  paused,
  last_sync_at, last_cycle:{actions, executed, failed, duration_ms},
  next_poll_at, last_error,
  guard:{blocked, reason},   // e.g. mass-delete needs --confirm-deletes
  watched_dirs, tracked_items, pending_ops,
  sync_dir, drive_folder, account, autostart }
```

## Monitoring store (no IPC)

The daemon records its own runtime state to the DB each cycle plus on a short heartbeat ticker, so a quiet daemon still looks alive and a dead one looks dead:

- **`daemon_status`** — singleton row (or reserved `meta` keys): `pid, started_at, last_heartbeat_at, mode, paused, last_sync_at, last_cycle_json, last_error, next_poll_at`. Heartbeat every ~10 s (independent of poll cadence). "running?" = socket ping succeeds, else `last_heartbeat_at` within ~3× the heartbeat interval → running; older → "stale (likely dead)".
- **`activity`** — capped ring table: `id, ts, kind, rel_path, detail`. v1 writes **cycle-level** summaries plus **failed actions** (cheap; the engine already returns `Result.Plan/Errors`). Per-action rows (touching the executor hot path) are optional and deferred — see open questions. Trim to the newest ~500 rows on insert.

Schema arrives as a **statedb migration v3** (`daemon_status`, `activity`), consistent with the existing v1/v2 migration mechanism and the "refuse a newer schema" guard.

### Lock discipline

Monitor and control CLI commands must **not** take the exclusive instance lock (the daemon holds it) — same as `status` today, which reads the DB without `openAppEnv`. Add a lock-free read env (or a read-only variant of `openAppEnv`) for `status`/`activity`/`config`/`account`, and have control commands talk only to the socket. A socket-delegated `sync` runs inside the daemon, which already holds the lock — no second acquisition.

## Autostart (service)

- **`service status`** — installed? loaded/enabled? running? via `launchctl list <label>` / `systemctl --user is-enabled`+`is-active` / `schtasks /Query /TN`. Fills the `autostart` field.
- Enable/disable-at-boot is the existing `service install`/`uninstall` (RunAtLoad / WantedBy / LogonTrigger). Optional `service enable`/`disable` to toggle autostart while keeping the unit installed — minor, deferred.

## Command surface (summary)

```
synckeeper status [--json] [--watch]   # live if daemon up (socket), else last-recorded (DB) + "last seen"
synckeeper activity [-n 50]            # recent actions from the activity ring
synckeeper sync                        # delegates to the daemon if running, else one-shot (today's behavior)
synckeeper pause | resume              # via control socket
synckeeper reload                      # re-read config.toml in the running daemon
synckeeper config                      # print effective config (+ how to edit)
synckeeper account                     # Google account, token presence/validity
synckeeper service status              # autostart installed / enabled / running
```

## Security

- Socket **0600**, owned by the user; filesystem perms are the entire auth model — appropriate for a single-user personal tool. Windows named pipe (if used) gets a security descriptor restricting to the current user.
- **No network listener, ever.** No token needed precisely because there is no TCP surface.
- Control is no more privileged than the user editing their own files. A socket-triggered `sync` still hits every guard — the mass-delete guard still blocks and the daemon still never self-confirms deletions; the block surfaces in `status` as `guard.blocked` for the human to clear with `sync --confirm-deletes`.

## Stages

1. **Monitoring, no IPC** — statedb v3 (`daemon_status`, `activity`); daemon heartbeat + activity recording; `status` becomes daemon-aware (`--json`, `--watch`); `service status`; `config`/`account` read views. Delivers the whole read-only list; zero new attack surface; works when the daemon is down.
2. **Control socket** — listener in `watch`; line-delimited JSON protocol; `sync` (delegated) / `pause` / `resume` / `reload`; CLI clients.
3. **Tray GUI** — separate cgo-allowed per-platform binary (`cmd/synckeeper-tray`) speaking the socket (with `subscribe` streaming); menu: status, last sync, Sync now, Pause/Resume, Open folder, Open logs, Quit; icon reflects mode. **Not** in `make build-all`.

## Open questions

- Persist `paused` across daemon restarts (meta flag) or reset to running on boot?
- Activity granularity: cycle-level + failures only (v1, no executor changes) vs. per-action rows (richer, touches the executor)?
- Protocol: stay line-delimited JSON, or move to HTTP-over-socket if/when a web UI is wanted?
- Windows IPC: AF_UNIX socket file (no dep) vs. `go-winio` named pipe (idiomatic, pure-Go dep)?
- Tray toolkit: `fyne.io/systray` (cgo on macOS) vs. platform-native — either way the GUI binary accepts cgo.

## Tasks

### Stage 1 — monitoring (no IPC) — done 2026-07-14
- [x] statedb migration v3: `daemon_status` (singleton) + `activity` (capped ring, 500 rows); accessors in `statedb/daemon.go`; "refuse newer schema" guard unchanged.
- [x] Daemon records heartbeat (10 s ticker, `watch.HeartbeatInterval`) + per-cycle summary; mode reflects watching / polling-only / backoff (paused is Stage 2). Recorder in `watch/status.go`.
- [x] Lock-free read env (`cmd/synckeeper/readenv.go`) for read-only commands — reads via SQLite WAL alongside the daemon's writer, no lock contention.
- [x] `status` daemon-aware: running/stale/stopped/never-run from heartbeat freshness, mode, last sync + cycle summary, next poll, guard-blocked, autostart; `--json` and `--watch`.
- [x] `activity [-n]`, `config` (effective config + edit hint), `account` (token presence/expiry/refresh — email lookup deferred, see decisions).
- [x] `service status` across launchd/systemd/Task Scheduler (`service.Status()` + factored parsers).
- [x] Tests: migration + daemon_status round-trip + activity ring cap (`statedb`); running Watcher records status + derives activity + marks stopped on shutdown (`watch`); `service status` parsers table-driven (`service`). Smoke-tested the rendered CLI output (never-run / running / stale / json).

Refinement vs. the original plan: activity is recorded **per interesting action derived from the successful cycle's plan** (upload/download/trash/move/mkdir/conflict), not merely cycle-level — this gives the Dropbox-like recent-activity list without touching the executor. Failed cycles record a single error entry (we can't tell which actions ran). `paused` mode is deferred to Stage 2 (it needs the control socket to toggle).

### Stage 2 — control socket — done 2026-07-14
- [x] Control listener in `watch`: Unix-domain socket / AF_UNIX (`net.Listen("unix", …)`, one path all OSes), 0600, stale-socket cleanup on start, unlink on shutdown. A failed bind (e.g. path over the ~104-char sun_path limit) logs a warning and the daemon runs on without control — sync is never blocked by it. `internal/control` (transport) + `watch/control.go` (handlers).
- [x] Line-delimited JSON, one request/response per connection, with a `v` protocol-version handshake that rejects mismatches loudly. Commands: `ping`, `sync{confirm_deletes}`, `pause`, `resume`, `reload`.
- [x] CLI clients: `sync` delegates to the daemon when it's up (waits for the cycle, prints the summary) else runs the one-shot as before; `pause`/`resume`/`reload`; `status` pings the socket for authoritative liveness.
- [x] `reload` hot-swaps poll interval / ignore / threshold / retention; reports cold fields (sync_dir, folder_name, machine_name) as needing a restart.
- [x] Tests: transport round-trip + version-mismatch + not-running detection (`control`); pause actually suppresses an auto-sync then resume syncs, sync-now runs even while paused, applyReload hot/cold split (`watch`, race-clean). End-to-end drive of the real daemon (ping/pause/resume/delegated-sync/reload/stopped) verified manually.

Deviations from the design sketch: **status/activity stay DB-sourced** rather than served over the socket — the DB is fresh within 10 s, works when the daemon is down, and avoids duplicating the render; the socket is used only for control plus a liveness `ping` that `status` folds in. **`sync --wait` is client-side**: the CLI polls the daemon's recorded status for completion (last-sync advance / cycle-summary change) rather than the server holding the connection open. `paused` is now a real mode (Stage 1 had deferred it).

### Stage 3 — tray GUI (separate binary)
- [ ] `cmd/synckeeper-tray` (or separate repo); own build target, cgo allowed; explicitly excluded from `make build-all`.
- [ ] `subscribe` streaming on the socket for live status.
- [ ] Menu + mode-reflecting icon; Sync now / Pause / Resume / Open folder / Open logs / Quit.
- [ ] Per-OS manual verification (icon, actions, reconnect when the daemon restarts).

## Verification

- [ ] Stage 1: with `watch` running, a second terminal's `status` shows live mode/last-sync/activity; kill the daemon → `status` flips to "stale/last seen"; `service status` correct on the dev machine.
- [ ] Stage 2: `sync`/`pause`/`resume`/`reload` reach the running daemon; a socket-triggered mass delete is still guard-blocked and shown in `status`.
- [ ] Stage 3: tray reflects state and drives the daemon on at least one OS; core `make build-all` still produces four pure-Go `CGO_ENABLED=0` binaries with the tray excluded.
