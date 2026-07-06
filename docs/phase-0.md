# Phase 0 — Skeleton (auth + plumbing)

**Goal:** repo compiles for all targets, `init` authenticates against Drive and persists everything later phases need, `status` proves it.
**Exit criterion:** authenticate and list the Drive folder on the dev machine; `make build-all` produces all four binaries.
**Status:** in progress — code complete 2026-07-06; remaining: Google Cloud prerequisites + live OAuth verification (manual, user)

## Prerequisites (manual, outside the repo)

- [ ] Create Google Cloud project; enable the Drive API.
- [ ] Create OAuth client of type **Desktop app**; note client id/secret.
- [ ] Publish the consent screen to **Production** (leave unverified). Do NOT leave it in Testing — refresh tokens would expire after 7 days.

## Tasks

### Repo bootstrap
- [x] `git init`; `.gitignore` (dist/, *.db, token.json).
- [x] `go mod init github.com/macsimbodnar/synckeeper`; pin Go 1.22+.
- [x] Directory skeleton per spec repo layout: `cmd/synckeeper/`, `internal/{config,auth,driveclient,statedb,guards}`.
  - Deviation: packages are created when they first get code (empty dirs aren't tracked by git); `scanner`, `remotedelta`, `reconcile`, `executor`, `conflicts`, `names` arrive in phase 1.
- [x] Makefile: `build`, `build-all` (linux-amd64, darwin-arm64, darwin-amd64, windows-amd64.exe), `test`, `vet`; `CGO_ENABLED=0` and `-trimpath -ldflags "-s -w -X main.version=$(VERSION)"` enforced.
- [x] Verify no transitive cgo requirement: `go build` with `CGO_ENABLED=0` for all four targets must succeed.

### Config (`internal/config`)
- [x] Config dir resolution via `os.UserConfigDir()` + `/synckeeper`; create on first use.
- [x] TOML load/validate for `config.toml` per spec (drive.folder_name, local.sync_dir with `~` expansion, engine.* keys with defaults). Unknown keys rejected to catch typos.
- [x] Write a default `config.toml` on `init` if absent.
- [x] Unit tests: defaults, validation errors, tilde expansion.

### Logging and CLI shell
- [x] `log/slog` structured logging: text handler to stderr, `--verbose` flag flips level to debug.
- [x] Cobra root command with `version`; `init`/`status` real; `sync`, `watch`, `doctor` stubs error "not implemented (phase N)".
- [x] Single-instance lock (`gofrs/flock` on `<config_dir>/lock`) acquired by every state-mutating command (currently: `init`).

### Auth (`internal/auth`)
- [x] Embedded OAuth client id/secret: `internal/auth/credentials.go` placeholders, overridable via `-ldflags -X`; clear runtime error while empty.
- [x] Loopback flow: localhost listener on ephemeral port, open browser (print URL as fallback), CSRF state check, exchange code. Live-tested only in the verification step below.
- [x] Persist token JSON at `<config_dir>/token.json` with 0600; verify perms on load.
- [x] Auto-refreshing `oauth2.TokenSource` that re-persists the token on refresh.

### Drive client (`internal/driveclient`)
- [x] Interface defined first: `List`, `Get`, `StartPageToken`, `Changes`, `Upload`, `Update`, `Download`, `Trash`, `Mkdir`, `Move`.
- [x] Real implementation wrapping `drive/v3` with the fields the spec requires (`id,name,mimeType,md5Checksum,size,version,parents,trashed`).
- [x] Internal retry helper: exponential backoff + jitter, max 5 attempts, on 403 rate/quota, 429, 5xx. Uploads deliberately not wrapped (media reader is consumed; hard failures stay in pending_ops).
- [x] Skeleton of the in-memory fake (structural ops done; changes-feed semantics grow in phase 1).

### State DB (`internal/statedb`)
- [x] Open/create `<config_dir>/state.db` via `modernc.org/sqlite`; `journal_mode=wal`.
- [x] Schema per spec (`items`, `meta`, `pending_ops`); `schema_version` in `meta`; migration scaffold (refuses DBs newer than the binary).
- [x] Typed accessors + write serialization (mutex around write transactions).
- [x] Unit tests against a temp-file DB.

### `synckeeper init`
- [x] Run auth flow; find folder named `drive.folder_name` at Drive root or create it; store `root_folder_id` in `meta`.
- [x] Fetch and store `changes.getStartPageToken`.
- [x] Create empty state DB; create local sync dir if missing. Stable random `machine_id` stored in `meta`.
- [x] Refuse to re-init over an existing DB without `--force`.

### `synckeeper status`
- [x] Show config summary, root folder id, DB item count, pending_ops count, token presence, quarantine size. (Skipped-items reporting arrives with the scanner in phase 1.)

## Verification

- [x] `make build-all` → four binaries in `dist/` (2026-07-06, CGO_ENABLED=0).
- [x] `go test ./...` green (2026-07-06).
- [x] Smoke: `status` reports uninitialized state cleanly; `init` without credentials fails with instructions; stubs exit non-zero.
- [ ] On dev machine: `synckeeper init` completes OAuth, creates the Drive folder; `synckeeper status` shows folder id and empty DB; a debug command or test lists the folder contents via the real client. **← blocked on the manual prerequisites above (user).**
