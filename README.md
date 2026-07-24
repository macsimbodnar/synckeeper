# Synckeeper

Personal bidirectional sync between one local folder (`~/Synckeeper`) and one Google Drive folder. Written in Go, daemon-first. Multiple machines sync independently against Drive as the hub. Built for one user — but with strict durability guarantees: three-way reconcile against a local SQLite baseline, atomic writes, trash/quarantine instead of permanent deletes, mass-delete guard, conflict copies (never last-writer-wins), crash-resumable operations.

**Status:** the build-out phases (0–7) are complete and retired; the primary-platform workstreams (correctness, fuzzer, FSEvents watcher, daemon-first polish) are done and the tool is daily-usable there. Current work is the real multi-machine rollout and the Linux/Windows ports — see [docs/status.md](docs/status.md) for the live last/current/next pointer and [docs/plan.md](docs/plan.md) for the backlog.

## Documentation

Start with [CLAUDE.md](CLAUDE.md) (the guide for agents and humans alike: read order, doc map, rules), then:

| Doc | Purpose |
|---|---|
| [MANUAL.md](MANUAL.md) | **User manual**: commands, configuration, behavior, known bugs |
| [docs/status.md](docs/status.md) | Live state: last / current / next task |
| [docs/spec.md](docs/spec.md) | Design doc & implementation contract (authoritative) |
| [docs/plan.md](docs/plan.md) | Workstream backlog + retired phase history |
| [docs/testing.md](docs/testing.md) | Acceptance ledger (scenario, fault, guard, regression) |
| [docs/decisions.md](docs/decisions.md) | Decision log: what, when, who, why |
| [docs/history/](docs/history/) | Archived per-phase checklists (read-only) |

## Build

Requires Go 1.26+ (see the `go` directive in `go.mod`). Binaries are built **natively on each target platform** (no cross-compile requirement; cgo permitted for OS-native integrations — see spec §10).

```sh
make build        # supported: native binary for the host platform → dist/ (cgo on → FSEvents on macOS, W3.2)
make build-all    # legacy pure-Go (CGO_ENABLED=0) cross-compile matrix; excludes the FSEvents backend (fsnotify fallback)
```

## Use

```sh
synckeeper init   # sign in with Google, create the Drive + local folders,
                  # and offer to keep syncing in the background at login
```

Everything else a user needs — the **full command reference, configuration (including bring-your-own OAuth client), sync behavior, recovery, and known bugs** — lives in **[MANUAL.md](MANUAL.md)**, kept true at every commit under its own rule (CLAUDE.md doc map). This README deliberately carries no usage sections beyond the quick start above; don't re-grow them here.

## Test

```sh
make test                             # go test ./... — all offline, uses in-memory Drive fake
SYNCKEEPER_LIVE_TEST=1 go test ./...  # additionally runs live smoke tests against a throwaway Drive folder
SYNCKEEPER_SOAK_SECONDS=7200 go test ./internal/watch/ -run TestSoak -timeout 3h   # 2-hour chaos soak
make audit                            # pre-publication secret-scan gate (also runs inside `make test`)
```

---

Keep this README's build/test sections correct at every commit — update it in the same change that alters a Makefile target or the build/test procedure. Everything user-facing lives in [MANUAL.md](MANUAL.md) under its own same-commit rule (see CLAUDE.md); usage sections do not belong here.
