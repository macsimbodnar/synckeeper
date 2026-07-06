# Synckeeper — Implementation Contract

This is the authoritative specification. The execution plan in [plan.md](plan.md) breaks it into tasks; when the two disagree, this file wins. Changes to scope or invariants are recorded in [decisions.md](decisions.md).

## What Synckeeper is

A personal bidirectional file sync tool written in Go. It keeps one designated local folder (e.g. `~/Synckeeper`) identical to one designated folder on a Google Drive account. Multiple desktop machines (Linux, macOS, Windows) each sync independently against Drive, which acts as the hub and the durable copy. Everything inside the folder is synced recursively; nothing outside it is ever touched. Files live as normal files on disk.

Built for a single user, so distribution polish, GUI, and Google verification are skipped. Data-integrity guarantees are NOT skipped: the engine must never silently lose or corrupt a file, even across crashes, offline edits on multiple machines, or a lost state database.

Deliverable per platform: a single statically linked binary, cross-compiled from one dev machine with `CGO_ENABLED=0`. Mobile is out of scope.

## Personal-use shortcuts (allowed)

1. Own Google Cloud project, OAuth "Desktop app" client, full `https://www.googleapis.com/auth/drive` scope. Publish the OAuth consent screen to **Production** but leave it **unverified** — click through the "unverified app" warning once per machine. Do NOT leave it in Testing status: refresh tokens in Testing expire after 7 days, which breaks a daemon.
2. CLI only. No GUI, no installer, no auto-update, no telemetry.
3. Token storage: JSON file with 0600 permissions in the config dir. No keyring integration in v1.
4. Google-native files (Docs, Sheets, Slides) are ignored and reported in `status`, never synced.
5. Remote change detection via polling `changes.list` (30–60 s), not push webhooks.
6. Files whose names are invalid on some platform are skipped and reported, not transliterated.
7. Local "trash" is a quarantine directory managed by Synckeeper, not OS-native trash.

## Non-negotiable durability invariants

1. Three-way reconcile against a persistent local baseline DB (SQLite). Never diff local vs remote directly.
2. Atomic local writes: download to temp file in same directory, fsync, atomic rename, then commit DB row. Never commit DB before the data is durable.
3. Deletes go to Drive trash (remote) and the local quarantine dir (local), never permanent.
4. Mass-delete guard: refuse to propagate deletion of more than a configurable fraction of tracked files without explicit confirmation. Treat "sync dir missing/unreadable" as an error, never as "everything deleted".
5. Conflicts produce a conflicted copy with machine name and date. Never last-writer-wins, never overwrite.
6. Every operation idempotent and crash-resumable. Killing the process at any point leaves a state a fresh run repairs.

## Language conventions

Go code follows standard Go conventions (gofmt, CamelCase identifiers, lowercase package names); snake_case is used everywhere it does not fight the language: file names, DB columns, config keys, CLI flags, JSON fields.

## Tech stack

- Go 1.22+. Single module `github.com/max/synckeeper` (adjust path).
- `CGO_ENABLED=0` enforced in the Makefile. Every dependency must be pure Go; reject any transitive cgo requirement.
- Dependencies:
  - `google.golang.org/api/drive/v3` and `google.golang.org/api/option` (official Drive client)
  - `golang.org/x/oauth2` + `golang.org/x/oauth2/google` (OAuth desktop loopback flow)
  - `modernc.org/sqlite` (pure-Go SQLite driver, via `database/sql`)
  - `github.com/fsnotify/fsnotify` (phase 3)
  - `github.com/spf13/cobra` (CLI)
  - `github.com/BurntSushi/toml` (config)
  - `github.com/gofrs/flock` (single-instance lock)
  - stdlib elsewhere: `log/slog`, `crypto/md5`, `net/http`, `os`, `path/filepath`
- Retry/backoff: small internal helper (exponential + jitter, max 5 attempts); no external dep.
- Config dir per OS via `os.UserConfigDir()`: `~/.config/synckeeper`, `~/Library/Application Support/synckeeper`, `%AppData%\synckeeper`. State DB at `<config_dir>/state.db`, token at `<config_dir>/token.json` (0600), quarantine at `<config_dir>/quarantine/`.

## Build and release

```make
BINARY = synckeeper
LDFLAGS = -s -w -X main.version=$(VERSION)

build-all:
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-amd64 ./cmd/synckeeper
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-arm64 ./cmd/synckeeper
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-amd64 ./cmd/synckeeper
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-windows-amd64.exe ./cmd/synckeeper
```

OAuth client id/secret for a Desktop-app client are not truly secret; embed them via `-ldflags -X` or a checked-in `credentials.go` in a private repo.

## Repo layout

```
cmd/synckeeper/main.go        # cobra root; wires commands
internal/config/              # TOML load/validate, config dir resolution
internal/auth/                # oauth loopback flow, token load/save/refresh
internal/driveclient/         # thin Drive v3 wrapper: list, changes, upload, download, trash, mkdir, move; retries/backoff live here; defined as an interface for test fakes
internal/statedb/             # sqlite schema, migrations, typed accessors, transactions
internal/scanner/             # local full scan and targeted rescan -> local snapshot
internal/remotedelta/         # changes.list consumption -> remote snapshot updates, tree membership tracking
internal/reconcile/           # pure function: (baseline, local_snapshot, remote_snapshot) -> action plan; no I/O, fully unit-testable
internal/executor/            # applies action plan with atomic write protocol and DB commits; pending_ops journal replay
internal/guards/              # mass-delete guard, missing-dir check, instance lock, quarantine management
internal/conflicts/           # conflict naming and materialization
internal/names/               # path <-> drive mapping, invalid name detection, case collision detection
internal/watch/               # phase 3: fsnotify watch manager + remote polling loop, debounce/coalesce
internal/service/             # phase 3: systemd/launchd/task scheduler wrapper generation
```

`reconcile` must stay pure (no filesystem, no network, no DB handles), taking snapshots in and returning a `[]Action`. This is the most-tested package.

## Config file (`config.toml`)

```toml
[drive]
folder_name = "Synckeeper"     # created at Drive root if absent on first init

[local]
sync_dir = "~/Synckeeper"

[engine]
poll_interval_secs = 45
full_rescan_interval_secs = 3600
mass_delete_threshold = 0.25   # fraction of tracked files
machine_name = "max_desktop"   # used in conflict filenames
quarantine_retention_days = 30
ignore = ["*.tmp", "~$*", ".DS_Store", "Thumbs.db", "*.swp", ".synckeeper*"]
```

## State DB schema

```sql
pragma journal_mode = wal;

create table items (
  drive_file_id   text primary key,
  rel_path        text not null unique,   -- posix-style, relative to sync root
  is_dir          integer not null,
  size            integer,
  content_md5     text,                   -- md5 of content as last synced
  local_mtime_ns  integer,                -- mtime observed after last sync
  drive_md5       text,                   -- md5Checksum reported by Drive at last sync
  drive_version   integer,                -- Drive 'version' field at last sync
  synced_at       integer not null
);

create table meta (
  key   text primary key,                 -- 'page_token', 'root_folder_id', 'machine_id', 'schema_version'
  value text not null
);

create table pending_ops (                -- journal for crash resume
  op_id         integer primary key autoincrement,
  op_type       text not null,            -- upload | download | trash_remote | quarantine_local | mkdir_remote | mkdir_local | move_remote | move_local
  rel_path      text,
  drive_file_id text,
  payload       text,                     -- json extras (temp path, target path, expected md5)
  state         text not null default 'planned'  -- planned | in_progress | done
);
```

Access through `database/sql` with a single writer goroutine or a mutex around write transactions (modernc/sqlite + WAL tolerates one writer).

## Reconcile algorithm

Inputs per item key (join on `drive_file_id` when known, else on `rel_path`):
- `base`: row from `items` (may be nil = new item).
- `loc`: local snapshot entry: exists, size, mtime_ns, md5 (compute md5 only when size or mtime differ from base; trust base otherwise).
- `rem`: remote snapshot entry: exists (not trashed), md5, version, parents, name.

Classify: `local_changed = loc differs from base`, `remote_changed = rem differs from base` (version or md5). Decision table:

| base | local | remote | action |
|---|---|---|---|
| absent | new | absent | upload, insert row |
| absent | absent | new | download, insert row |
| absent | new | new, same md5 | adopt: record row, no transfer |
| absent | new | new, diff md5 | conflict: rename local to conflicted copy, download remote |
| present | unchanged | unchanged | nothing |
| present | changed | unchanged | upload new revision |
| present | unchanged | changed | download (atomic replace) |
| present | changed | changed, same md5 | record, no transfer |
| present | changed | changed, diff md5 | conflict: local becomes conflicted copy, remote wins the canonical name |
| present | deleted | unchanged | trash remote, delete row (guarded) |
| present | unchanged | trashed/deleted | quarantine local file, delete row |
| present | changed | trashed | resurrect: re-upload local as new file (edit beats delete) |
| present | deleted | changed | download remote (edit beats delete) |

Moves/renames:
- Remote: same `drive_file_id`, new name or parent inside the tree → local rename/move. Moved out of tree → treat as remote delete. Moved into tree → treat as remote create.
- Local: scanner sees delete+create. Before executing, pair deletions and creations with identical md5 and size; convert pairs into `move_remote` (files.update with new name/parent) to avoid re-upload. Unpaired remain delete+create.

Directories: created before children, trashed only when empty after children resolved. Delete-folder vs add-file-inside resolves as resurrect the folder.

Ordering: mkdirs (top-down), moves, uploads/downloads, deletes (bottom-up). All actions written to `pending_ops` first, marked `in_progress` when started, `done` plus `items` update in one transaction when finished. On startup, re-verify and replay any non-done ops (idempotent: check current state before acting).

Concurrency: transfers may run in a small worker pool (e.g. 4 goroutines) but plan generation and DB commits are serialized; two actions touching the same rel_path or ancestor/descendant paths never run concurrently.

## Atomic write protocol (downloads and replacements)

1. Download to `.synckeeper.tmp.<random>` in the destination directory, streaming, computing md5 on the fly.
2. Verify md5 against Drive's `md5Checksum`.
3. `File.Sync()`, close, `os.Rename` onto the target (atomic on same filesystem; on Windows use rename-with-replace semantics: remove-then-rename guarded by the pending_ops journal, or `MoveFileEx` semantics via `os.Rename` which replaces on modern Go/Windows). Fsync the parent directory on POSIX.
4. Stat the result, then commit the `items` row with observed mtime_ns in the same transaction that marks the op done.

Uploads: hash the file before upload; upload with chunked resumable media (`googleapi.ChunkSize(8MB)`) for files > 5 MB, simple upload otherwise. Only update `items` after Drive returns the new `version` and `md5Checksum` and it matches the pre-upload hash. If the local file's mtime changed during upload, mark dirty and requeue.

## Guards and quarantine

- `mass_delete_guard`: if a planned batch trashes/quarantines more than `mass_delete_threshold` of tracked files (and more than 10 absolute), abort with a report; require `synckeeper sync --confirm-deletes`.
- `sync_dir` missing, empty-but-DB-nonempty, or unreadable: hard error, no plan executed.
- Instance lock via `gofrs/flock` on `<config_dir>/lock`.
- Quarantine: local deletions move files to `<config_dir>/quarantine/<YYYY-MM-DD>/<rel_path>` preserving structure; entries older than `quarantine_retention_days` are purged at the end of a successful sync. `synckeeper status` lists quarantine size.

## Drive API usage

- Scope: `https://www.googleapis.com/auth/drive`.
- Auth: `oauth2` loopback flow (spin up localhost listener, open browser, exchange code). Token JSON persisted 0600; auto-refresh via `oauth2.TokenSource`, re-persist on refresh.
- Initial walk: `Files.List` with `q = "'<parent_id>' in parents and trashed = false"`, fields `id,name,mimeType,md5Checksum,size,version,parents`, paginate.
- Deltas: `Changes.List(pageToken)` with `includeRemoved=true`; maintain an in-DB parent map to answer "is this change inside the tree". Store the new page token only after the batch is fully processed.
- Upload: `Files.Create` / `Files.Update` with `Media(...)`.
- Download: `Files.Get(id).Download()` streaming.
- Delete: `Files.Update` setting `trashed=true`. Never `Files.Delete`.
- Skip any file whose mimeType starts with `application/vnd.google-apps` (report in status).
- Retries: exponential backoff with jitter on 403 rate/quota, 429, 5xx; max 5 attempts; hard failures stay in `pending_ops` for the next run.
- Duplicate names in one Drive folder: keep the first by id, report the rest as quarantined (do not download duplicates in v1).

## Conflict naming

`<stem> (conflict <machine_name> <YYYY-MM-DD_HHMMSS>)<suffix>`, e.g. `notes (conflict max_desktop 2026-07-06_142200).md`. The conflicted copy is uploaded too, so every machine sees both versions.

## CLI (cobra)

- `synckeeper init [--adopt]`: auth, find/create Drive folder, create DB, store start page token. `--adopt` performs first-merge on a non-empty Drive folder plus non-empty local dir: union of both, md5-equal pairs adopted, differing pairs to conflict copies, nothing deleted.
- `synckeeper sync [--dry-run] [--confirm-deletes]`
- `synckeeper watch`
- `synckeeper status`
- `synckeeper doctor [--repair]`
- `synckeeper service install|uninstall` (phase 3)

## Testing strategy

`go test ./...`. `driveclient` is an interface; tests use an in-memory fake implementing the same semantics (ids, versions, md5, trash, changes feed). A small live smoke suite runs only when `SYNCKEEPER_LIVE_TEST=1` against a throwaway Drive folder. `reconcile` gets table-driven tests covering the full decision matrix. Fault injection via an executor hook that panics/exits at named checkpoints.

The full scenario/fault/guard/platform test matrix lives in [testing.md](testing.md).

## Explicit non-goals for v1

GUI, installer, Google verification/CASA, selective sync, bandwidth limits, file versioning UI (Drive's own revision history is the backstop), shared drives, multiple sync folders, mobile.
