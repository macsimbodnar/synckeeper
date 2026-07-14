# Synckeeper

Personal bidirectional sync between one local folder (`~/Synckeeper`) and one Google Drive folder. Written in Go; single static binary per platform (Linux, macOS, Windows). Multiple machines sync independently against Drive as the hub. Built for one user — no GUI, no installer — but with strict durability guarantees: three-way reconcile against a local SQLite baseline, atomic writes, trash/quarantine instead of permanent deletes, mass-delete guard, conflict copies (never last-writer-wins), crash-resumable operations.

**Status:** phase 3 complete (2-hour soak passed); phase 6 stage 1 (daemon monitoring) done. Implemented: crash-safe `sync`, continuous `watch` (fsnotify + polling), `doctor [--repair]`, `service install`/`status` login-service wrappers, and daemon-aware `status`/`activity`/`config`/`account`. Next: phase 4 (`init --adopt`, multi-machine). See [docs/plan.md](docs/plan.md) for phase status.

## Documentation

| Doc | Purpose |
|---|---|
| [docs/spec.md](docs/spec.md) | Implementation contract (authoritative) |
| [docs/plan.md](docs/plan.md) | Execution plan, phase status, working process |
| [docs/phase-0.md](docs/phase-0.md) … [phase-6.md](docs/phase-6.md) | Per-phase task checklists |
| [docs/testing.md](docs/testing.md) | Test matrix (scenario, fault, guard, platform) |
| [docs/decisions.md](docs/decisions.md) | Decision log |

## Build

Requires Go 1.22+. No cgo — everything cross-compiles from one machine.

```sh
make build        # binary for the host platform → dist/
make build-all    # linux-amd64, darwin-arm64, darwin-amd64, windows-amd64 → dist/
```

## Run

```sh
synckeeper init             # OAuth flow, find/create Drive folder, create state DB
synckeeper init --adopt     # join an existing non-empty Drive folder (safe first merge)
synckeeper sync             # one-shot bidirectional sync
synckeeper sync --dry-run   # print the plan, change nothing
synckeeper watch            # continuous mode (fsnotify + remote polling)
synckeeper status           # daemon state, config, counts, recent activity (--json, --watch)
synckeeper activity [-n 20] # recent actions recorded by the watch daemon
synckeeper config           # print the effective configuration
synckeeper account          # Google credential status (token presence/expiry)
synckeeper doctor [--repair]
synckeeper service install|uninstall|status   # run watch as a login service (launchd/systemd/Task Scheduler)
```

Config lives at the platform config dir (`~/.config/synckeeper` on Linux, `~/Library/Application Support/synckeeper` on macOS, `%AppData%\synckeeper` on Windows): `config.toml`, `state.db`, `token.json`, `quarantine/`.

First-time setup needs a personal Google Cloud project with the Drive API enabled and a Desktop-app OAuth client, consent screen published to **Production** (unverified is fine; Testing status expires tokens in 7 days). See [docs/phase-0.md](docs/phase-0.md).

## Test

```sh
make test                             # go test ./... — all offline, uses in-memory Drive fake
SYNCKEEPER_LIVE_TEST=1 go test ./...  # additionally runs live smoke tests against a throwaway Drive folder
SYNCKEEPER_SOAK_SECONDS=7200 go test ./internal/watch/ -run TestSoak -timeout 3h   # 2-hour chaos soak
```

---

Keep this README's build/run/test sections correct at every commit — update it in the same change that alters a command or Makefile target.
