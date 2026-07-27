# Synckeeper

Personal bidirectional sync: one local folder (`~/Synckeeper`) ↔ one Google Drive folder. Go, daemon-first. Many machines sync independently against Drive as the hub. Built for one user, with strict durability: three-way reconcile against a local SQLite baseline, atomic writes, Drive bin + system trash instead of permanent deletes, mass-delete guard, conflict copies (never last-writer-wins), crash-resumable ops. Non-negotiable invariants: [spec §3](docs/spec.md).

**Status:** build-out phases (0–7) complete and retired; the primary-platform workstreams (correctness, fuzzer, FSEvents watcher, daemon-first polish) are done and the tool is daily-usable there. Current: real multi-machine rollout + the Linux/Windows ports. Live pointer: [docs/status.md](docs/status.md); backlog: [docs/plan.md](docs/plan.md).

## Documentation

Start with [CLAUDE.md](CLAUDE.md) (guide for agents and humans: read order, doc map, rules), then:

| Doc | Purpose |
|---|---|
| [MANUAL.md](MANUAL.md) | **End-user manual**: commands, config, behavior, known bugs |
| [docs/status.md](docs/status.md) | Live state: last / current / next |
| [docs/spec.md](docs/spec.md) | Design doc & implementation contract (authoritative) |
| [docs/plan.md](docs/plan.md) | Workstream backlog + retired phase history |
| [docs/testing.md](docs/testing.md) | Acceptance ledger (scenario, fault, guard, regression) |
| [docs/decisions.md](docs/decisions.md) | Decision log: what, when, who, why |
| [docs/history/](docs/history/) | Archived per-phase checklists (read-only) |

## Usage

Installing, commands, config, sync behavior, recovery, known bugs → **[MANUAL.md](MANUAL.md)** (end-user manual, kept true every commit). This README is developer-facing and carries no usage instructions.

## Layout

The daemon is the product; the CLI (and any future UI) are thin clients of it. One serialized loop owns the engine; `reconcile` is a pure function. Details: [spec.md](docs/spec.md).

| Path | Role |
|---|---|
| `cmd/synckeeper` | CLI entry (cobra); thin client of the daemon |
| `internal/engine` | one sync cycle: scan → reconcile → execute → commit baseline |
| `internal/reconcile` | pure `(base, local, remote) → ordered plan`; decision table + dependency ordering (spec §4). Most-tested code |
| `internal/executor` | runs the plan; the local-write gate — the single local-mutation choke point (spec §7) |
| `internal/scanner` | local tree scan (md5 only on size/mtime drift) |
| `internal/remotedelta` | Drive metadata mirror from `changes.list`; case/normalization fold |
| `internal/driveclient` | Drive API client + in-memory fake (offline tests) |
| `internal/statedb` | SQLite baseline/state (items, meta, pending_ops, remote_nodes, daemon_status, activity); WAL |
| `internal/watch` | the daemon: fswatch backends (FSEvents/fsnotify), control socket, status recorder |
| `internal/control` | control-socket protocol (line-delimited JSON; client + server) |
| `internal/status` | read model for every read-only view: `Snapshot`/`Gather` (machine reads behind seams), the human + JSON renderers, and the display formatters `status`/`info`/`doctor`/`activity` share |
| `internal/tui` | the live dashboard behind `status` on a terminal (bubbletea + lipgloss): pure `Update`/`View`, three views, golden-tested without a tty; read-only by construction (AST guard) |
| `internal/auth` | OAuth loopback + S256 PKCE; token at `0600`; credential resolve (requires `credentials.json`; no embedded default) |
| `internal/config` | TOML load + validate |
| `internal/conflicts` | conflict-copy naming |
| `internal/names` | name mapping, case/normalization fold, validity rules |
| `internal/guards` | mass-delete guard, sync-dir sanity, instance lock |
| `internal/service` | login service (launchd / systemd / Task Scheduler); log-perm hardening |
| `internal/trash` | OS trash module: freedesktop (Linux, pure Go), `NSFileManager` (macOS, cgo), unavailable elsewhere; `Fake` bin for tests |
| `internal/doctor` | DB ↔ disk ↔ Drive cross-check + additive repair |
| `internal/audit` | pre-publication secret-scan gate (`make audit`) |

## Build

Go 1.26+ (see the `go` directive in `go.mod`). Built **natively per platform** (no cross-compile requirement; cgo permitted for OS-native integrations — spec §10).

```sh
make build        # supported: native host binary → dist/ (cgo on → FSEvents on macOS, W3.2)
make build-all    # legacy pure-Go (CGO_ENABLED=0) cross-compile matrix (excludes FSEvents; fsnotify fallback)
```

## Test

```sh
make test        # go test ./... — offline, in-memory Drive fake; includes the secret-scan gate
make vet         # go vet ./...
make audit       # pre-publication secret-scan gate (also runs inside make test)
go test -race ./...
make clean       # remove dist/
```

Env-gated suites (exact invocations in [CLAUDE.md](CLAUDE.md)): `SYNCKEEPER_LIVE_TEST` (live smoke vs a throwaway Drive folder), `SYNCKEEPER_SOAK_SECONDS` (chaos soak), `SYNCKEEPER_SCALE_FILES` (≥50k watcher scale), `SYNCKEEPER_FUZZ_*` (randomized convergence).

---

Keep this README's build/test/layout correct at every commit — same change that alters a Makefile target, the build/test procedure, or the package layout. User-facing content lives in [MANUAL.md](MANUAL.md) under its own same-commit rule; usage sections don't belong here.
