# Synckeeper

Personal bidirectional sync between one local folder (`~/Synckeeper`) and one Google Drive folder. Written in Go, daemon-first. Multiple machines sync independently against Drive as the hub. Built for one user — but with strict durability guarantees: three-way reconcile against a local SQLite baseline, atomic writes, trash/quarantine instead of permanent deletes, mass-delete guard, conflict copies (never last-writer-wins), crash-resumable operations.

**Status:** the build-out phases (0–7) are complete and retired; the tool is daily-usable on the primary platform. Current work is organized in workstreams (correctness fixes first) — see [docs/status.md](docs/status.md) for the live last/current/next pointer and [docs/plan.md](docs/plan.md) for the backlog.

## Documentation

Start with [CLAUDE.md](CLAUDE.md) (the guide for agents and humans alike: read order, doc map, rules), then:

| Doc | Purpose |
|---|---|
| [docs/status.md](docs/status.md) | Live state: last / current / next task |
| [docs/spec.md](docs/spec.md) | Design doc & implementation contract (authoritative) |
| [docs/plan.md](docs/plan.md) | Workstream backlog + retired phase history |
| [docs/testing.md](docs/testing.md) | Acceptance ledger (scenario, fault, guard, regression) |
| [docs/decisions.md](docs/decisions.md) | Decision log: what, when, who, why |
| [docs/history/](docs/history/) | Archived per-phase checklists (read-only) |

## Build

Requires Go 1.22+. Binaries are built **natively on each target platform** (no cross-compile requirement; cgo permitted for OS-native integrations — see spec §10).

```sh
make build        # binary for the host platform → dist/
make build-all    # legacy cross-compile matrix; still works until cgo lands (workstream W3)
```

## Run

```sh
synckeeper init             # OAuth flow, find/create Drive folder, create state DB
synckeeper init --adopt     # join an existing non-empty Drive folder (safe first merge)
synckeeper login            # re-authenticate (refresh an expired/revoked token); stop the daemon first
synckeeper sync             # one-shot bidirectional sync
synckeeper sync --dry-run   # print the plan, change nothing
synckeeper watch            # continuous mode (fsnotify + remote polling)
synckeeper status           # daemon state, config, counts, recent activity (--json, --watch)
synckeeper activity [-n 20] # recent actions recorded by the watch daemon
synckeeper config           # print the effective configuration
synckeeper account          # Google credential status (token presence/expiry)
synckeeper sync             # delegates to the running daemon if up, else one-shot
synckeeper pause | resume   # suspend / resume automatic syncing in the daemon
synckeeper reload           # re-read config.toml in the running daemon
synckeeper doctor [--repair]
synckeeper service install|uninstall|status   # run watch as a login service (launchd/systemd/Task Scheduler)
```

Config lives at the platform config dir (`~/.config/synckeeper` on Linux, `~/Library/Application Support/synckeeper` on macOS, `%AppData%\synckeeper` on Windows): `config.toml`, `state.db`, `token.json`, `quarantine/`.

First-time setup needs a personal Google Cloud project with the Drive API enabled and a Desktop-app OAuth client, consent screen published to **Production** (unverified is fine; Testing status expires tokens in 7 days). See [docs/history/phase-0.md](docs/history/phase-0.md). (Planned, spec §9: released binaries will ship with embedded default credentials, with bring-your-own as an override for dedicated quota.)

## Test

```sh
make test                             # go test ./... — all offline, uses in-memory Drive fake
SYNCKEEPER_LIVE_TEST=1 go test ./...  # additionally runs live smoke tests against a throwaway Drive folder
SYNCKEEPER_SOAK_SECONDS=7200 go test ./internal/watch/ -run TestSoak -timeout 3h   # 2-hour chaos soak
```

---

Keep this README's build/run/test sections correct at every commit — update it in the same change that alters a command or Makefile target.
