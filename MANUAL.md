# Synckeeper — User Manual

How to *use* Synckeeper. (Build / dev / design docs → [README.md](README.md).)

Keeps **one local folder** ↔ **one Google Drive folder** identical, both directions. Many machines sync against the same Drive folder (the hub + durable copy). Runs as a background daemon; **never silently loses or corrupts a file**: deletes → Drive bin + your system bin (never permanent); conflicting edits → conflict copy (never last-writer-wins); edit always beats delete.

**Repo rule: updated in the same commit as any change to commands, config, user-visible behavior, or known bugs.** Last updated: 2026-07-27.

---

## 1. Install and first sync

Needs a binary for your machine (README — `make build`, Go 1.26+; macOS primary today) **and your own Google OAuth `credentials.json`** in the config dir — set that up **first** (§5).

```sh
synckeeper init          # opens your browser for Google sign-in, creates the
                         # Drive folder ("Synckeeper") and the local one (~/Synckeeper),
                         # then offers to keep syncing at login
```

`init` ends with **`Start Synckeeper automatically at login? [Y/n]`** — yes installs the login service (background sync from now on). `--service` / `--no-service` skip the prompt (scripts/pipes get no prompt; it prints how to install later).

Done. Files in `~/Synckeeper` appear in Drive and on your other machines; changes anywhere converge everywhere.

Declined? Install later with `synckeeper service install`, or run `synckeeper watch` (foreground, same behavior), or `synckeeper sync` for a one-shot.

First sign-in on your own unpublished client shows Google's **"unverified app" warning** — expected. Continue past it, or publish your client to Production (§5).

## 2. Adding another machine

Second machine, after installing the binary:

```sh
synckeeper init --adopt      # join the existing non-empty Drive folder,
                             # then offers to keep syncing at login
```

Same login-service offer as a first-machine `init`. `--adopt` union-merges: local-only → upload, remote-only → download, identical → pair up, differ on both → conflict copy. **Adoption never deletes.** Plain `init` refuses a non-empty Drive folder and points at `--adopt`.

## 3. Command reference

Global flags: `-v` / `--verbose` — debug logging; `--version` — print version; `-h` / `--help` — usage help.

| Command | What it does |
|---|---|
| `init [--force] [--adopt] [--service\|--no-service]` | Authenticate, find/create the Drive folder, create local state, then offer the login service (§1). Takes the instance lock — stop the login service first if it's running (§7). `--force`: re-init over an existing state DB. `--adopt`: §2. `--service`/`--no-service`: install/skip the login service without prompting. |
| `login` | Re-authenticate with Google (fresh browser flow, replaces the stored token). Stop the daemon first — `login` takes the instance lock, because a running daemon holds the old token in memory. |
| `sync [--dry-run] [--confirm-deletes]` | One-shot sync. If the daemon runs, delegated to it and awaited. `--dry-run`: print the plan, change nothing (needs daemon stopped). `--confirm-deletes`: §6. |
| `watch` | Run continuously, foreground: local changes picked up under a second, remote within the poll interval. |
| `status [--plain] [--json] [--interval D]` | **Human:** on a terminal, the live dashboard (§10). **Scriptable:** piped/redirected it prints the one-shot report — daemon state, last sync, config, guard blocks, where deletions from Drive land (system bin or quarantine), recent activity; `--plain` forces that report on a terminal, `--json` emits it for scripts. Works daemon up or down. `--interval` sets how often the dashboard re-reads state (default 1s; it redraws ~4×/s regardless). |
| `activity [-n N]` | **Scriptable.** Last N (default 20) synced items with direction (local→drive / drive→local / conflict). The dashboard's activity view (§10) is the interactive equivalent. |
| `pause` / `resume` | Suspend / resume automatic syncing in the running daemon. Explicit `sync` still works while paused. Pause doesn't survive a daemon restart. |
| `reload` | Re-read `config.toml` in the running daemon. Hot fields apply live; identity fields reported as needing a restart (§4). |
| `config` | Print the effective config and the file it's read from. |
| `account` | Token status, which OAuth client is active (embedded default or your own), and the signed-in Google account (email via one `about.get`; online only, skipped offline). |
| `info [--json]` | **Scriptable** (and the pre-`init` dump). One-shot static snapshot: version; every config-file path (config dir, `config.toml`, `state.db`, `token.json`, `credentials.json` — both with their permission mode, flagged when other users can read them; `control.sock`, quarantine, system bin, log); sync dir; Drive folder + id; machine name + id; OAuth client; token status; effective config; local state (tracked items, pending ops, quarantine). Read-only, offline (email → `account`), works before `init`. `--json` for scripts. |
| `doctor [--repair]` | Cross-check state DB vs disk vs Drive; reports the system-bin destination. `--repair` rebuilds lost metadata and re-adopts matching files — only ever *adds*; never deletes, quarantines, or overwrites. |
| `service install\|uninstall\|status` | Manage the login service running `watch` (launchd on macOS; logs to `~/Library/Logs/synckeeper.log`, kept owner-only `0600` since it records synced file names). `install` then checks whether the daemon actually started and names the likely cause (e.g. missing `credentials.json`) if not. |
| `help [command]` | Usage help for Synckeeper or a specific command (built-in). |
| `completion <bash\|zsh\|fish\|powershell>` | Print a shell autocompletion script (built-in); `synckeeper completion <shell> --help` shows how to install it. |

## 4. Configuration

`config.toml` lives in the per-OS config dir — macOS `~/Library/Application Support/synckeeper/`, Linux `~/.config/synckeeper/`, Windows `%AppData%\synckeeper\` — alongside `state.db`, `token.json`, the quarantine folder, the control socket, and the optional `credentials.json` (§5). Unknown keys rejected (typo protection); missing keys use the defaults shown.

```toml
[drive]
folder_name = "Synckeeper"       # Drive folder (created at Drive root if absent) — restart

[local]
sync_dir = "~/Synckeeper"        # the synced folder — restart

[engine]
poll_interval_secs = 45          # how often remote changes are polled — hot
mass_delete_threshold = 0.25     # only where deletions are unrecoverable, see §6 — hot
machine_name = "max_mbp"         # appears in conflict-copy names — restart
quarantine_retention_days = 30   # rescue copies kept this long — hot
ignore = ["*.tmp", "~$*", ".DS_Store", "Thumbs.db", "*.swp", ".synckeeper*"]  # hot
```

**Hot** fields apply live via `synckeeper reload`; **restart** fields need a daemon restart. Ignore patterns match the file *name* only (not the path), both directions; ignored files don't sync.

## 5. Google credentials (required)

Synckeeper ships with **no credentials** — you supply your own Google Cloud "Desktop app" OAuth client. One-time setup, **before** `synckeeper init`:

1. Create a Google Cloud project, enable the **Drive API**.
2. Add an OAuth client of type **Desktop app** ([Google guide](https://developers.google.com/workspace/guides/create-credentials#oauth-client-id)).
3. Publish the consent screen to **Production** — unverified is fine, but **Testing** status expires refresh tokens after 7 days (kills the daemon).
4. Download the client JSON, place it in the config dir (§4) as `credentials.json`, exactly as downloaded — no editing:

```sh
cp ~/Downloads/client_secret_*.json "$HOME/Library/Application Support/synckeeper/credentials.json"
```

Keep it private: `chmod 600 credentials.json`. Synckeeper tightens the config dir itself (`0700`) on every run, and `info` flags a `credentials.json` other local users can read — it warns rather than refuses, since the file is yours.

Then run `synckeeper init` (first time — it signs in and offers the login service) or `synckeeper login` (re-point an existing install). **Don't `service install` before signing in** — the service runs `watch`, which can't sign in by itself, so it will just crash-loop until you've authenticated. To (re-)authenticate later, the daemon must be stopped first: `synckeeper service uninstall`, sign in, then reinstall (§7). Lookup order: `credentials.json` → optional build-time `-ldflags` injection → else a "no OAuth client credentials" error. `synckeeper account` shows the active client. `credentials.json` is yours — it stays gitignored, never committed.

## 6. How syncing behaves

- **Conflicts.** Same file changed both sides → remote keeps the name, your local version kept beside it as `name (conflict <machine> <date_time>).ext` — and uploaded too, so every machine sees both. Nothing lost.
- **Deletes never permanent.** Deleted in Drive → the local copy goes to **your system bin** (macOS Trash / Linux desktop trash), where you can restore it like anything else you deleted; deleted locally → the Drive file goes to Drive's bin (restorable ~30 days).
- **A deleted folder is one bin entry, both ways.** A folder deleted in Drive arrives in your bin as that folder, contents inside, restorable in one go — not as one entry per file. A folder you delete locally likewise goes into Drive's bin as one folder. If anything inside it changed since the scan, or a file the sync never saw is in there, the folder is removed file by file instead and the stranger is left alone.
- **No system bin? Quarantine.** Where the OS offers no trash (unsupported platform, a build without it, a bin that refuses the move), the copy goes to the dated quarantine folder (`<config dir>/quarantine/<date>/…`, kept `quarantine_retention_days` days) exactly as before. `activity` names the one that was used (`trash` vs `quarantine`); `info` shows the bin destination.
- **Edit beats delete, always.** Deleted one side, edited the other → the edit survives and comes back.
- **Moves/renames synced as moves**, files and folders alike: the Drive file/folder keeps its identity and history; a folder rename travels as one operation. (Exception: renaming an *empty* folder syncs as delete + recreate — no contents as evidence — costing only the folder's Drive-side id.)
- **Mass-delete guard — only when a deletion can't be undone.** Deleting a lot is not held back: everything you delete is one restore away, in Drive's bin or your own. The guard fires only where that isn't true — **a machine with no system bin**, where the local copies fall back to the quarantine folder — and then only past >25% of your files and >10 absolute. There, a one-shot `sync` stops and asks for `--confirm-deletes`, the daemon keeps syncing everything else and shows the block in `status` until you confirm, and it never self-confirms. `status`/`doctor` say plainly when your machine has no bin. **A large deletion that *did* run is reported**, not hidden: a `deleted` line in `activity` with the count and where to restore from.
- **Sanity guard (not confirmable).** Sync folder missing, unreadable, or suddenly empty while files are tracked → syncing stops with an error. No flag overrides it: an unmounted disk is never a deletion.
- **Not synced (skipped — reported in `sync` output, not `status`):** Google-native files (Docs/Sheets/Slides), symlinks, non-regular files, filesystem-invalid names, Drive same-name duplicates in one folder (first wins, rest skipped). Listed by a one-shot `synckeeper sync` (and `init --adopt`); the daemon doesn't surface them in `status`. Ignored patterns skipped silently.
- **Crash safe.** Interrupt at any point (crash, kill, power loss) → recovered next run; partial transfers discarded and replanned.

## 7. Recovery

| Problem | Fix |
|---|---|
| Daemon logs auth failures / token expired or revoked | Stop the daemon → `synckeeper login` → restart it. |
| `login`/`init` says "another instance is running" | The running service daemon holds the instance lock. `synckeeper service uninstall` (or Ctrl-C a `watch` terminal), run the `login`/`init`, then reinstall the service. |
| Service crash-loops with "no OAuth client credentials" | Place your `credentials.json` (§5). The service runs `watch`, which can't sign in by itself: if you've never signed in, `synckeeper service uninstall` → `synckeeper init` → reinstall. |
| State DB lost or corrupted | `synckeeper doctor --repair` — rebuilds metadata and re-adopts matching files; next `sync` re-uploads/downloads the rest. Never deletes. |
| Need a deleted file back | Check your system bin (a folder comes back whole), then Drive's bin. On a platform with no system bin, the quarantine folder (`<config dir>/quarantine/<date>/…`). Bins are not forever — Drive's empties after ~30 days, and your desktop's may too. |
| Deleted a lot and want to see what went | `synckeeper activity` — a large deletion leaves a `deleted` line with the count and the destination. |
| Something looks off | `synckeeper status -v`, then `synckeeper doctor` for a full cross-check. |

## 8. Known bugs

Confirmed and reproduced. The adversarial correctness rounds (W1.7–W1.9, all done) retired every other entry; what remains is the deferred "directory arm" of the local-write gate — case-only names and renames for *directories and existing files*, a recorded follow-up in [docs/decisions.md](docs/decisions.md). Two facets:

- **Same-name folder in different case/accents on two machines can mint a duplicate folder in Drive** (W1.9.1 follow-up). *New* files with such names resolve as ordinary conflicts or adopts, and a name collision can no longer send anything to quarantine. *Workaround: avoid folder names differing only in case/accents.*
- **Renaming an existing file to differ only in case/accents (`a.txt` → `A.txt`) makes your *other* machines retry that rename every cycle** — `status` shows a repeated failure; the file stays safe on both sides, its name just doesn't update elsewhere. Same deferred root cause (the case-only-rename arm of the gate). *Workaround: also edit the file's contents when you change its case — it then converges after a transient retry instead of looping.*

- **Under investigation (found 2026-07-25 on Linux): a folder deleted in Drive can be *re-uploaded* by another machine instead of being removed.** Observed once on a freshly adopted Linux machine: a tracked folder moved to the Drive bin was not removed locally, and the daemon then attempted to upload part of the tree back to Drive. Every one of those uploads **failed** and nothing reached Drive (they showed as `error upload …: no such file or directory` in `activity` — the local-write gate refusing files that no longer existed). The same deletion propagated correctly on the other (macOS) machine, and the normal path is covered by a passing regression test, so the trigger is machine-state-specific and not yet identified — the suspect is a `init --adopt` that was still running when the login service first tried to start, leaving part of the tree on disk but untracked. (The mass-delete guard was investigated as part of this and found correct; it has since been narrowed — §6 — so it no longer holds recoverable deletions at all.) *Nothing was lost* (Drive's bin and the other machine's rescue copy both held the content; that machine used the quarantine, which was the destination before the system bin replaced it). *If you hit it: check `synckeeper status` on every machine before deleting a large folder, and prefer deleting it on a machine's filesystem rather than in the Drive web UI until this is fixed.*

## 9. Known limitations (by design)

- One folder, one Google account, one Drive folder. No selective sync, no bandwidth limits, no shared drives.
- Whole-file transfers (Drive has no delta API): a 1-byte change re-transfers the file.
- Remote changes arrive within the poll interval (default 45 s); local changes sync under a second while the daemon runs. If file watching is unavailable (e.g. out of file descriptors), the daemon runs polling-only — everything still syncs, local changes just wait for the next poll — and restores watching automatically; `status` shows the mode.
- Requires your own Google OAuth client (§5) — no credentials are bundled. Your client's consent screen: use **Production** (Testing expires refresh tokens in 7 days); an unpublished client shows a one-time "unverified app" warning.
- **Full-Drive access scope.** Authorizes with Google's full `drive` scope, not folder-limited — Google has no folder-scoped OAuth scope, and the only narrower option (`drive.file`) would silently stop syncing files added to the Drive folder from *outside* Synckeeper (Drive web UI, other apps). So the stored token can reach your whole Drive; kept at `token.json` `0600`, only ever sent to Google over HTTPS. May be revisited.
- All files kept on disk (no online-only placeholders).
- macOS primary; Linux and Windows planned (code written portably, not yet validated there).

## 10. Live dashboard (`synckeeper status`)

`synckeeper status` on a terminal is a live, read-only dashboard — it holds no lock, never writes the database, and does no syncing. Piped or with `--plain` you get the one-shot report instead; `--json` is unchanged for scripts.

Three views, switched with number keys or Tab:

| View | Shows |
|---|---|
| **1 overview** | next poll (an estimate — see below), last sync + cycle summary, tracked/pending/quarantine counts, where deletions from Drive land, and an **attention** block that appears only when something needs it (guard block + how to release it, last error, no system bin, dead daemon, missing credentials, autostart not installed), then recent activity |
| **2 activity** | the recorded history (500 entries), newest first, with direction and file count. Scroll it, filter by direction (`f`), or search paths/details/kinds (`/`) — the heading reports `N of M` and what is filtering |
| **3 info** | the static picture — version, every path with permission modes, sync target + Drive folder id, machine name + id, OAuth client, token status, effective config. Same data `info` prints |

| Keys | |
|---|---|
| `1` `2` `3` · `Tab` · `←` `→` | select / cycle views |
| `j` `k` · `↑` `↓` | scroll the activity list |
| `PgUp` `PgDn` · `Space` | scroll a page |
| `g` `G` | jump to newest / oldest |
| `f` | filter activity: all → local→drive → drive→local → conflict → errors |
| `/` | search paths, details and kinds — `Enter` keeps it, `Esc` cancels |
| `c` | clear filter and search |
| `s` | sync now (asks the running daemon) |
| `p` / `P` | pause / resume automatic syncing |
| `R` | reload `config.toml` in the daemon |
| `?` · `q`/`Ctrl-C` | help · quit |

Colour follows `NO_COLOR` and `TERM=dumb`. `--interval` sets how often state is re-read (default 1s); the frame redraws ~4×/s regardless, so counters keep ticking between reads even at a long interval.

`s`/`p`/`P`/`R` are the same requests `sync`, `pause`, `resume` and `reload` send; a result line appears under the header and clears itself. **`s` never confirms a held mass deletion** — a deletion the guard is holding is released only by `synckeeper sync --confirm-deletes` on the command line (§6), never by a keystroke.

**The countdown is exact when the daemon can tell you.** A running daemon publishes its real poll deadline, what a running cycle is doing (`syncing  checking Drive · 45s`, or `transferring · 1,145 actions · 8s`), whether local changes are already in flight (`next sync  3 local changes — syncing` — a bound on what is about to sync, since one save can fire several events), and which watch backend is live — shown in the header, e.g. `watching (FSEvents)`. If the daemon is older than the dashboard, or not running, the figure falls back to the database and is **marked `≈`**: that number is recorded when a cycle ends, while the poll timer runs independently and any local change pre-empts it, so the real next sync is usually *sooner*.
