# Synckeeper — User Manual

Everything you need to *use* Synckeeper. (For building, developing, or the design docs, start at [README.md](README.md).)

Synckeeper keeps **one local folder** and **one Google Drive folder** identical, in both directions. Several machines can each sync against the same Drive folder, which acts as the hub and the durable copy. It is built to run as a background daemon and to **never silently lose or corrupt a file**: deletes go to Drive trash and a local quarantine (never permanent), conflicting edits produce a conflict copy (never last-writer-wins), and an edit always beats a delete.

**This file is maintained under a repo rule: it is updated in the same commit as any change to commands, configuration, user-visible behavior, or the known-bug list.** Last updated: 2026-07-18.

---

## 1. Install and first sync

Requires a binary built for your machine (see README — `make build`, Go 1.26+; macOS is the primary platform today).

```sh
synckeeper init          # opens your browser for Google sign-in, creates the
                         # Drive folder ("Synckeeper") and the local one (~/Synckeeper)
synckeeper service install   # run at login, keep syncing in the background
```

That's it. Files you put in `~/Synckeeper` appear in Drive and on your other machines; changes made anywhere converge everywhere.

If you'd rather not install the login service, run `synckeeper watch` in a terminal (same behavior, foreground), or call `synckeeper sync` manually whenever you want a one-shot sync.

On first sign-in Google shows an **"unverified app" warning** — expected: the app ships with the author's unverified OAuth client. Continue past it, or use your own client (§5).

## 2. Adding another machine

On the second machine, after installing the binary:

```sh
synckeeper init --adopt      # join the existing non-empty Drive folder
synckeeper service install
```

`--adopt` merges both sides as a union: local-only files upload, remote-only files download, identical files pair up, and files that differ on both sides become a conflict copy. **Adoption can never delete anything.** Plain `init` refuses a non-empty Drive folder and points you at `--adopt`.

## 3. Command reference

Global flag: `-v` / `--verbose` — debug logging.

| Command | What it does |
|---|---|
| `init [--force] [--adopt]` | Authenticate, find or create the Drive folder, create local state. `--force` re-initializes over an existing state DB. `--adopt`: §2. |
| `login` | Re-authenticate with Google (fresh browser flow, replaces the stored token). Stop the daemon first — `login` takes the instance lock on purpose, because a running daemon holds the old token in memory. |
| `sync [--dry-run] [--confirm-deletes]` | One-shot sync. If the daemon is running, the sync is delegated to it and awaited. `--dry-run` prints the plan without changing anything (needs the daemon stopped). `--confirm-deletes`: §6. |
| `watch` | Run continuously in the foreground: local changes are picked up in under a second, remote changes within the poll interval. |
| `status [--json] [--watch]` | Daemon state, last sync, configuration, guard blocks, recent activity. Works whether or not the daemon runs. `--watch` refreshes until interrupted. |
| `activity [-n N]` | The last N (default 20) synced items with direction (local→drive / drive→local / conflict). |
| `pause` / `resume` | Suspend / resume automatic syncing in the running daemon. An explicit `sync` still works while paused. Pause does not survive a daemon restart. |
| `reload` | Re-read `config.toml` in the running daemon. Hot fields apply live; identity fields are reported as needing a restart (§4). |
| `config` | Print the effective configuration and where it came from. |
| `account` | Token status and which OAuth client is in use (embedded default or your own). |
| `doctor [--repair]` | Cross-check state DB vs disk vs Drive. `--repair` rebuilds lost metadata and re-adopts matching files — it only ever *adds*; it never deletes, quarantines, or overwrites. |
| `service install\|uninstall\|status` | Manage the login service that runs `watch` (launchd on macOS; logs to `~/Library/Logs/synckeeper.log`). |

## 4. Configuration

`config.toml` lives in the per-OS config dir — macOS: `~/Library/Application Support/synckeeper/`, Linux: `~/.config/synckeeper/`, Windows: `%AppData%\synckeeper\` — alongside the state DB (`state.db`), token (`token.json`), quarantine folder, control socket, and the optional `credentials.json` (§5). Unknown keys are rejected (typo protection); missing keys use the defaults shown.

```toml
[drive]
folder_name = "Synckeeper"       # Drive folder (created at Drive root if absent) — restart

[local]
sync_dir = "~/Synckeeper"        # the synced folder — restart

[engine]
poll_interval_secs = 45          # how often remote changes are polled — hot
mass_delete_threshold = 0.25     # fraction of tracked files, see §6 — hot
machine_name = "max_mbp"         # appears in conflict-copy names — restart
quarantine_retention_days = 30   # rescue copies kept this long — hot
ignore = ["*.tmp", "~$*", ".DS_Store", "Thumbs.db", "*.swp", ".synckeeper*"]  # hot
```

**Hot** fields apply live via `synckeeper reload`; **restart** fields need the daemon restarted. Ignore patterns match the file *name* only (not the path) and apply to both directions; ignored files simply don't sync.

## 5. Using your own Google client (optional)

Synckeeper ships with the author's OAuth client embedded, so there is nothing to set up — `synckeeper init` just works. All default-credential users share one Google Cloud project's Drive-API quota; per-user rate limits keep them isolated from each other, but a heavy user may want more headroom.

**Bring your own client (for dedicated quota):**

1. Create a personal Google Cloud project and enable the **Drive API**.
2. Add an OAuth client of type **Desktop app**.
3. Publish the consent screen to **Production** — unverified is fine, but **Testing** status expires refresh tokens after 7 days, which kills the daemon.
4. Download the client JSON and drop it in the config dir (§4) as `credentials.json`, exactly as downloaded — no editing:

```sh
cp ~/Downloads/client_secret_*.json "$HOME/Library/Application Support/synckeeper/credentials.json"
synckeeper login          # re-authenticate against your own client
synckeeper account        # confirms: oauth client: credentials.json in the config dir
```

Lookup order is `credentials.json` in the config dir → the embedded default. A desktop-app client secret is not truly confidential (it ships in every binary), so this file is not sensitive — but it is still yours. `synckeeper account` always shows which client is active.

## 6. How syncing behaves

- **Conflicts.** When the same file changed on both sides, the remote version keeps the name and your local version is preserved next to it as `name (conflict <machine> <date_time>).ext` — and uploaded too, so every machine sees both. Nothing is lost either way.
- **Deletes are never permanent.** A remote delete moves the local file to the quarantine (`<config dir>/quarantine/<date>/…`, kept `quarantine_retention_days` days); a local delete moves the Drive file to Drive's trash (restorable ~30 days).
- **Edit beats delete, always.** If a file is deleted on one side and edited on the other, the edited version survives and comes back.
- **Moves and renames of files** are synced as moves (the Drive file keeps its identity and history). *Folder* renames currently are not — see Known bugs.
- **The mass-delete guard.** A plan that would delete more than 25% of your files (and more than 10) is held back: a one-shot `sync` stops and asks for `--confirm-deletes`; the daemon keeps syncing everything else and shows the block in `status` until you confirm with `synckeeper sync --confirm-deletes`. The daemon never confirms deletions by itself.
- **Sanity guard.** If the sync folder is missing, unreadable, or suddenly empty while files are tracked (an unmounted disk looks exactly like "everything was deleted"), syncing stops with an error instead of propagating deletions.
- **Not synced (skipped, shown in `status`):** Google-native files (Docs/Sheets/Slides), symlinks, non-regular files, names invalid on your filesystem, and Drive same-name duplicates in one folder (first one wins, rest are skipped). Ignored patterns are skipped silently.
- **Crash safe.** Interrupting the process at any point (crash, kill, power loss) is recovered on the next run; partial transfers are discarded and replanned.

## 7. Recovery

| Problem | Fix |
|---|---|
| Daemon logs auth failures / token expired or revoked | Stop the daemon → `synckeeper login` → start it again. |
| State DB lost or corrupted | `synckeeper doctor --repair` — rebuilds metadata and re-adopts matching files; next `sync` re-uploads/downloads the rest. Never deletes. |
| Need a deleted file back | Check the quarantine folder (`<config dir>/quarantine/<date>/…`) and Drive's trash. |
| Something looks off | `synckeeper status -v`, then `synckeeper doctor` for a full cross-check. |

## 8. Known bugs

Confirmed, reproduced, and scheduled — tracked in [docs/plan.md](docs/plan.md) (W1.8/W1.9); this list shrinks as fixes land.

- **Renaming a local folder syncs as delete + recreate** (W1.8.2). The folder's Drive identity churns (Drive-side sharing/history of the folder is lost), and renaming a large folder tree can trip the mass-delete guard: the daemon then holds the deletions and waits — check `status`, then `synckeeper sync --confirm-deletes`. File contents are safe throughout.
- **Editing a file seconds after renaming it can leave the edit unsynced** (W1.8.2/R18) — the rename can capture stale content state, and the edit is then never uploaded. *Workaround: re-save (or `touch`) the file — any later change re-syncs it.*
- **Same name with different case/accents created on two machines mis-resolves** (W1.9.1). E.g. `Readme.txt` here and `README.txt` there: a case-duplicate can appear in Drive and the local file may end up in quarantine instead of becoming a conflict copy. *Workaround: avoid names differing only in case; recover from quarantine + Drive.*
- **A folder deleted remotely can get stuck locally if it contains an ignored file** (W1.9.2), typically a `.DS_Store`: the daemon repeats a `directory not empty` error. *Workaround: delete the leftover file yourself.*
- **Two quarantines of the same path on the same day keep only the newer rescue copy** (W1.9.3). The older version is still in Drive's trash.
- **A name that is a file on one side and a folder on the other** causes a repeating error and can create a stray same-name entry in Drive (W1.9.4). *Workaround: rename one of the two.*
- **`reload` during heavy file activity has a race** (W1.8.6). *Workaround: `pause`, `reload`, `resume`.*
- **The daemon can exit instead of degrading to polling** if the file watcher fails to rebuild under file-descriptor pressure (W1.8.7). In service mode launchd restarts it automatically, so this self-heals; in foreground `watch` you'd have to restart it.

## 9. Known limitations (by design)

- One folder, one Google account, one Drive folder. No selective sync, no bandwidth limits, no shared drives.
- Whole-file transfers (Drive has no delta API): a 1-byte change re-transfers the file.
- Remote changes arrive within the poll interval (default 45 s); local changes sync in under a second while the daemon runs.
- The default OAuth client is unverified (consent warning, ~100-user cap) — §5 for your own client.
- All files are kept on disk (no online-only placeholders).
- macOS is the primary platform; Linux and Windows are planned (the code is written portably but not yet validated there).
