# Phase 0 — Skeleton (auth + plumbing)

**Goal:** repo compiles for all targets, `init` authenticates against Drive and persists everything later phases need, `status` proves it.
**Exit criterion:** authenticate and list the Drive folder on the dev machine; `make build-all` produces all four binaries.
**Status:** not started

## Prerequisites (manual, outside the repo)

- [ ] Create Google Cloud project; enable the Drive API.
- [ ] Create OAuth client of type **Desktop app**; note client id/secret.
- [ ] Publish the consent screen to **Production** (leave unverified). Do NOT leave it in Testing — refresh tokens would expire after 7 days.

## Tasks

### Repo bootstrap
- [ ] `git init`; `.gitignore` (dist/, *.db, token.json).
- [ ] `go mod init github.com/max/synckeeper` (adjust module path); pin Go 1.22+.
- [ ] Directory skeleton per spec repo layout: `cmd/synckeeper/`, `internal/{config,auth,driveclient,statedb,scanner,remotedelta,reconcile,executor,guards,conflicts,names}`.
- [ ] Makefile: `build`, `build-all` (linux-amd64, darwin-arm64, darwin-amd64, windows-amd64.exe), `test`, `lint`; `CGO_ENABLED=0` and `-trimpath -ldflags "-s -w -X main.version=$(VERSION)"` enforced.
- [ ] Verify no transitive cgo requirement: `go build` with `CGO_ENABLED=0` for all four targets must succeed.

### Config (`internal/config`)
- [ ] Config dir resolution via `os.UserConfigDir()` + `/synckeeper`; create on first use.
- [ ] TOML load/validate for `config.toml` per spec (drive.folder_name, local.sync_dir with `~` expansion, engine.* keys with defaults).
- [ ] Write a default `config.toml` on `init` if absent.
- [ ] Unit tests: defaults, validation errors, tilde expansion.

### Logging and CLI shell
- [ ] `log/slog` structured logging: text handler to stderr, `--verbose` flag flips level to debug.
- [ ] Cobra root command with `version`; subcommand stubs for `init`, `sync`, `status`, `doctor` (stubs error "not implemented" except init/status).
- [ ] Single-instance lock (`gofrs/flock` on `<config_dir>/lock`) acquired by every state-mutating command.

### Auth (`internal/auth`)
- [ ] Embedded OAuth client id/secret (checked-in `credentials.go`; repo stays private).
- [ ] Loopback flow: localhost listener on ephemeral port, open browser (print URL as fallback), exchange code.
- [ ] Persist token JSON at `<config_dir>/token.json` with 0600; verify perms on load.
- [ ] Auto-refreshing `oauth2.TokenSource` that re-persists the token on refresh.

### Drive client (`internal/driveclient`)
- [ ] Define the interface first: `List`, `Changes`, `GetStartPageToken`, `Upload`, `Update`, `Download`, `Trash`, `Mkdir`, `Move`, `Get`.
- [ ] Real implementation wrapping `drive/v3` with the fields the spec requires (`id,name,mimeType,md5Checksum,size,version,parents`).
- [ ] Internal retry helper: exponential backoff + jitter, max 5 attempts, on 403 rate/quota, 429, 5xx.
- [ ] Skeleton of the in-memory fake (full semantics grow in phase 1).

### State DB (`internal/statedb`)
- [ ] Open/create `<config_dir>/state.db` via `modernc.org/sqlite`; `journal_mode=wal`.
- [ ] Schema per spec (`items`, `meta`, `pending_ops`); `schema_version` in `meta`; migration scaffold.
- [ ] Typed accessors + write serialization (mutex around write transactions).
- [ ] Unit tests against a temp-file DB.

### `synckeeper init`
- [ ] Run auth flow; find folder named `drive.folder_name` at Drive root or create it; store `root_folder_id` in `meta`.
- [ ] Fetch and store `changes.getStartPageToken`.
- [ ] Create empty state DB; create local sync dir if missing.
- [ ] Refuse to re-init over an existing DB without `--force`.

### `synckeeper status`
- [ ] Show config summary, root folder id, DB item count, pending_ops count, skipped/reported items, quarantine size (0 for now).

## Verification

- [ ] `make build-all` → four binaries in `dist/`.
- [ ] `go test ./...` green.
- [ ] On dev machine: `synckeeper init` completes OAuth, creates the Drive folder; `synckeeper status` shows folder id and empty DB; a debug command or test lists the folder contents via the real client.
