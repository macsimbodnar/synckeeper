# Synckeeper

Personal bidirectional sync: one local folder (`~/Synckeeper`) ↔ one Google Drive folder. Go, daemon-first. Many machines sync independently against Drive as the hub.

Built for one user, durability first: three-way reconcile against a local SQLite baseline, atomic writes, Drive bin + system trash instead of permanent deletes, mass-delete guard, conflict copies (never last-writer-wins), crash-resumable ops. Non-negotiable invariants: [spec §3](docs/spec.md).

**Installing and using it → [MANUAL.md](MANUAL.md).** This file is developer-facing and carries no usage instructions.

**Status:** build-out phases (0–7) retired; daily-usable on macOS and Linux. Current: W18 (remaining correctness items), then W7 (Linux port close-out) and W6 (multi-machine rollout) — [docs/status.md](docs/status.md).

## Docs

Start with [CLAUDE.md](CLAUDE.md) — read order, doc map, rules for agents and humans.

| Doc | Purpose |
|---|---|
| [MANUAL.md](MANUAL.md) | End-user manual: setup, commands, config, behavior, known bugs |
| [docs/status.md](docs/status.md) | Live state: last / current / next |
| [docs/spec.md](docs/spec.md) | Design doc & implementation contract (authoritative) |
| [docs/plan.md](docs/plan.md) | Workstream backlog + retired phase history |
| [docs/testing.md](docs/testing.md) | Acceptance ledger |
| [docs/decisions.md](docs/decisions.md) | Decision log: what, when, who, why |
| [docs/history/](docs/history/) | Archived per-phase checklists (read-only) |

## Layout

The daemon is the product; the CLI (and any future UI) are thin clients of it. One serialized loop owns the engine; `reconcile` is a pure function. Details: [spec.md](docs/spec.md).

| Path | Role |
|---|---|
| `cmd/synckeeper` | CLI entry (cobra); thin client of the daemon |
| `internal/engine` | one sync cycle: scan → reconcile → execute → commit baseline |
| `internal/reconcile` | pure `(base, local, remote) → ordered plan`; decision table + dependency ordering (spec §4). Most-tested code |
| `internal/executor` | runs the plan; the single local-mutation choke point (spec §7) |
| `internal/scanner` | local tree scan (md5 only on size/mtime drift) |
| `internal/remotedelta` | Drive metadata mirror from `changes.list`; case/normalization fold |
| `internal/root` | Drive folder identity: id-first resolve, rename recorded, recreate + baseline reset when gone (never a deletion) |
| `internal/driveclient` | Drive API client + in-memory fake (offline tests) |
| `internal/statedb` | SQLite baseline/state (items, meta, pending_ops, remote_nodes, daemon_status, activity); WAL |
| `internal/watch` | the daemon: fswatch backends (FSEvents/fsnotify), control socket, status recorder |
| `internal/control` | control-socket protocol (line-delimited JSON): client, server, the read-only `stat` query |
| `internal/status` | read model behind every read-only view: `Snapshot`/`Gather`, human + JSON renderers, shared display formatters |
| `internal/tui` | live dashboard behind `status` (bubbletea + lipgloss): pure `Update`/`View`, golden-tested without a tty, read-only by AST guard |
| `internal/auth` | OAuth loopback + S256 PKCE; token at `0600`; credential resolve (requires `credentials.json`, no embedded default) |
| `internal/config` | TOML load + validate |
| `internal/conflicts` | conflict-copy naming |
| `internal/names` | name mapping, case/normalization fold, validity rules |
| `internal/guards` | mass-delete guard, sync-dir sanity, instance lock |
| `internal/service` | login service (launchd / systemd / Task Scheduler); log-perm hardening |
| `internal/trash` | OS trash: freedesktop (Linux, pure Go), `NSFileManager` (macOS, cgo), unavailable elsewhere; `Fake` for tests |
| `internal/doctor` | DB ↔ disk ↔ Drive cross-check + additive repair |
| `internal/audit` | pre-publication secret-scan gate (`make audit`) |

## Build & test

Go 1.26+ (see `go.mod`). Built **natively per platform** — no cross-compile requirement; cgo permitted for OS-native integrations ([spec §10](docs/spec.md)).

```sh
make build        # native host binary → dist/ (cgo on → FSEvents on macOS)
make build-all    # legacy pure-Go (CGO_ENABLED=0) cross-compile matrix; fsnotify fallback
make test         # go test ./... — offline, in-memory Drive fake; includes the secret-scan gate
make vet          # go vet ./...
make audit        # secret-scan gate on its own
make clean        # remove dist/
go test -race ./...
```

Env-gated suites, exact invocations in [CLAUDE.md](CLAUDE.md): `SYNCKEEPER_LIVE_TEST` (live smoke vs a throwaway Drive folder), `SYNCKEEPER_SOAK_SECONDS` (chaos soak), `SYNCKEEPER_SCALE_FILES` (≥50k watcher scale), `SYNCKEEPER_FUZZ_*` (randomized convergence).
