# Synckeeper — User Manual

One local folder ↔ one Google Drive folder, both directions, on any number of machines. Runs as a background daemon. Build/dev docs → [README.md](README.md).

**Never silently loses or corrupts a file.** Deletes go to Drive's bin *and* your system bin, never permanent. Conflicting edits make a conflict copy, never last-writer-wins. An edit always beats a delete.

**Setup, in order:**

1. **Google credentials** — [§5](#5-google-credentials-required). One time, ~5 min. Nothing works without it.
2. `synckeeper init` — [§1](#1-install-and-first-sync).
3. Done. It syncs in the background.

---

## 1. Install and first sync

Needs a binary for your machine (README → `make build`) and your own `credentials.json` in place (§5).

```sh
synckeeper init
```

Asks **`Machine name [<hostname>]:`** — the label conflict copies carry on every machine (§4); empty accepts the default, `--machine-name N` skips the prompt. Then signs you in via the browser, creates the Drive folder (`Synckeeper`) and the local one (`~/Synckeeper`), merges both sides, and asks **`Start Synckeeper automatically at login? [Y/n]`**; `--service` / `--no-service` skip that prompt.

Scripts and pipes are never prompted: the machine name takes its default, the service is not installed (a hint is printed). Re-running `init` defaults to the machine name already in `config.toml`, not the hostname. No usable hostname on a first run → `unknown_machine`, printed with the fix (`engine.machine_name`, §4).

**`init` never deletes, and is safe to re-run.** Union merge: local-only → upload, remote-only → download, identical → paired up, different → conflict copy. At `init` only, a differing pair gives the plain name to the side edited more recently; the other becomes the conflict copy. Both survive everywhere.

Declined the service? Install it later with `synckeeper service install`, or run `synckeeper watch` (foreground, same behavior) or `synckeeper sync` (one-shot).

First sign-in shows Google's **"unverified app" warning** — expected, continue past it (§5).

## 2. Adding another machine

Install the binary, copy your `credentials.json` (§5), then:

```sh
synckeeper init
```

Same command as the first machine — there is no separate "join" mode. It merges both sides and never deletes, so re-running is always safe.

Copy `credentials.json` between your machines. **Never copy `token.json`** — each machine signs in for itself (§5).

## 3. Command reference

Global flags: `-v` / `--verbose` (debug logging), `--version`, `-h` / `--help`.

| Command | What it does |
|---|---|
| `init [--machine-name N] [--service\|--no-service]` | Set up or re-sync this machine: ask the machine name (§1), sign in, resolve the Drive folder, merge both sides (nothing deleted), offer the login service (§1). `--machine-name` skips that prompt. Safe to re-run — also the fix for a lost state DB (§7). Takes the instance lock: stop the service first (§7). |
| `login` | Fresh browser sign-in, replacing the stored token. No lock, no restart — a running daemon adopts the new token on its next sync. |
| `sync [--dry-run] [--confirm-deletes]` | One-shot sync; delegated to the daemon and awaited if one runs. `--dry-run` prints the plan and changes nothing (needs the daemon stopped). `--confirm-deletes`: §6. |
| `watch` | Run continuously in the foreground. |
| `status [--plain] [--json] [--interval D]` | On a terminal, the live dashboard (§10). Piped or with `--plain`, a one-shot report — daemon state, last sync, config, guard blocks, bin destination, recent activity; `--json` for scripts. Works daemon up or down. `--interval` sets the state re-read period (default 1s). |
| `activity [-n\|--number N]` | Last N (default 20) synced items with direction: local→drive / drive→local / conflict. |
| `pause` / `resume` | Suspend / resume automatic syncing. Explicit `sync` still works while paused. Not persisted across a daemon restart. |
| `reload` | Re-read `config.toml` in the running daemon; identity fields are reported as needing a restart (§4). |
| `config` | Print the effective config and the file it came from. |
| `account` | Which OAuth client is active and its client **id**, token status, and the signed-in Google account (needs network; skipped offline). |
| `info [--json]` | Static snapshot: version; every path with its permission mode (flagged when other users can read it); sync dir; Drive folder + id; machine name + id; OAuth client; token status; effective config; tracked / pending / quarantine counts. Read-only, offline, works before `init`. |
| `doctor [--repair]` | Cross-check state DB vs disk vs Drive. Names any Drive folder holding **the same name twice** — Drive allows it, a filesystem doesn't, so only the first is synced; fix it in the Drive web UI. Takes the instance lock (§7). `--repair` only ever *adds*: rebuilds lost metadata, re-adopts matching files; never deletes, quarantines, or overwrites. |
| `service install\|uninstall\|status` | Manage the login service running `watch` — **launchd** on macOS, a **systemd user unit** on Linux, Task Scheduler on Windows (§4). `install` checks the daemon actually started and names the likely cause if not. On Linux it syncs **while you are logged in** and stops at logout (§9). |
| `help [command]` | Usage help. |
| `completion <bash\|zsh\|fish\|powershell>` | Shell completion script; `synckeeper completion <shell> --help` shows how to install it. |

## 4. Configuration

`config.toml` lives in the per-OS config dir, alongside `state.db`, `token.json`, `credentials.json` (§5), the quarantine folder and the control socket:

| OS | Config dir |
|---|---|
| macOS | `~/Library/Application Support/synckeeper/` |
| Linux | `~/.config/synckeeper/` |
| Windows | `%AppData%\synckeeper\` |

The login service and its log live outside it:

| OS | Service | Log |
|---|---|---|
| macOS | launchd, `~/Library/LaunchAgents/com.mazerfaker.synckeeper.plist` | `~/Library/Logs/synckeeper.log`, kept `0600` — it records file names |
| Linux | systemd user unit, `~/.config/systemd/user/synckeeper.service` | `$XDG_STATE_HOME/synckeeper/synckeeper.log`, default `~/.local/state/synckeeper/synckeeper.log`, kept `0600` — it records file names. `journalctl --user -u synckeeper`: unit lifecycle only |
| Windows | Task Scheduler | not redirected |

Neither log is rotated or capped.

Unknown keys are rejected (typo protection); missing keys use these defaults.

```toml
[drive]
folder_name = "Synckeeper"       # Drive folder (created at Drive root if absent) — restart

[local]
sync_dir = "~/Synckeeper"        # the synced folder — restart

[engine]
poll_interval_secs = 45          # how often remote changes are polled — hot
mass_delete_threshold = 0.25     # only where deletions are unrecoverable, see §6 — hot
machine_name = "max_mbp"         # conflict-copy names; asked by init — restart
quarantine_retention_days = 30   # rescue copies kept this long — hot
ignore = ["*.tmp", "~$*", ".DS_Store", "Thumbs.db", "*.swp", ".synckeeper*"]  # hot
```

**Hot** fields apply live via `synckeeper reload`; **restart** fields need a daemon restart. Ignore patterns match the file *name* only (not the path), in both directions.

## 5. Google credentials (required)

Synckeeper bundles no credentials — you use your own Google OAuth client. Do this **before** `synckeeper init`.

### Create the client

1. **Project** — [console.cloud.google.com](https://console.cloud.google.com/) → create a project (any name).
2. **Enable the API** — *APIs & Services* → *Library* → **Google Drive API** → *Enable*.
3. **Consent screen** — *APIs & Services* → *OAuth consent screen*. User type **External**, fill in app name and your own email.
4. **Publish it** — set the publishing status to **In production**. Not optional: a client left in **Testing** expires its refresh tokens after **7 days**, which kills the daemon weekly.
5. **Create the client** — *Credentials* → *Create credentials* → *OAuth client ID* → application type **Desktop app** → *Create* → **Download JSON**. ([Google's guide](https://developers.google.com/workspace/guides/create-credentials#oauth-client-id).) It must be an OAuth client — not an API key, not a service-account key.

### Install it

Save the file **exactly as downloaded**, no editing, as `credentials.json` in your config dir (§4):

```sh
cp ~/Downloads/client_secret_*.json "$HOME/Library/Application Support/synckeeper/credentials.json"
chmod 600 "$HOME/Library/Application Support/synckeeper/credentials.json"
```

The `chmod` is a step, not a nicety: downloads arrive `0644`, readable by every user on the machine.

Check it, then sign in:

```sh
synckeeper account      # → oauth client: credentials.json in the config dir
synckeeper init         # first machine; `synckeeper login` re-points an existing install
```

`synckeeper info` shows the file's path and mode, and flags it when others can read it.

### Expect the "unverified app" warning

Publishing to production does **not** remove it. Synckeeper asks for Google's full `drive` scope, which is *restricted*, so the warning is gated on Google verification (an annual paid third-party security assessment) rather than on publishing. Continue past it. The practical cost of staying unverified is a 100-user cap per project — irrelevant for personal use.

### Keep them yours

- **`token.json` is the sensitive file**, not `credentials.json`. It grants access to your Drive. Never copy it off the machine, never share it. Each machine signs in for itself.
- **`credentials.json` is safe to copy to your own machines** and nowhere else. A desktop-app client secret is not a real secret (it ships inside every desktop app; [RFC 8252](https://datatracker.ietf.org/doc/html/rfc8252)), but sharing yours means sharing your project's quota and one revocation blast radius.
- **Never commit either.** Both are gitignored here (`credentials.json`, `client_secret_*.json`, `token.json`); GitHub push protection will also block a client secret.
- Synckeeper forces the config dir to `0700` on every run. Keep `sync_dir` well away from it (§9).
- **Revoke:** [myaccount.google.com/permissions](https://myaccount.google.com/permissions) drops the app's access to your account. Deleting the OAuth client in the console invalidates `credentials.json` on every machine.

### Notes

- The daemon can't sign in by itself — don't `service install` before your first `init`.
- Re-authenticating later needs no stop and no restart: `synckeeper login` with the daemon running, then `synckeeper sync` to apply it immediately instead of after the retry wait (§6).
- Lookup order: `credentials.json` → an optional build-time `-ldflags` client → otherwise a "no OAuth client credentials" error.

## 6. How syncing behaves

- **Conflicts.** Changed on both sides → remote keeps the name, your version is kept beside it as `name (conflict <machine> <date_time>).ext` and uploaded too, so every machine sees both. Nothing lost.
- **Except at `init`**, where the more recently edited side takes the plain name and the other becomes the conflict copy (renamed on Drive too). Badly skewed clocks can misname a pair at `init` — never lose one.
- **Deletes are never permanent.** Deleted in Drive → your local copy goes to **your system bin** (macOS Trash, Linux desktop trash), restorable like anything else. Deleted locally → the Drive file goes to Drive's bin (~30 days).
- **A deleted folder is one bin entry, both ways** — contents inside, restorable in one go. If something inside changed since the scan, or holds a file the sync never saw, the folder is removed file by file instead and the stranger is left alone.
- **No system bin? Quarantine.** Where the OS offers none (unsupported platform, a build without it, a bin that refuses the move), the copy goes to `<config dir>/quarantine/<date>/`, kept `quarantine_retention_days`. `activity` names which was used; `info` shows the destination.
- **An edit always beats a delete.** Deleted one side, edited the other → the edit survives and comes back.
- **A folder Synckeeper can't read is skipped, not fatal.** The rest of the tree keeps syncing; its contents are left alone on **both** sides, never treated as deleted. Reported in `sync` output, one daemon warning per folder. Fix the permissions and it resumes by itself.
- **Renaming the Drive folder is safe** — it's tracked by Drive id. `folder_name` only names the folder if Synckeeper has to create one.
- **Moves and renames sync as moves**, files and folders alike; a folder rename travels as one operation. Exception: renaming an *empty* folder syncs as delete + recreate, costing only that folder's Drive id.
- **Mass-delete guard — only where a deletion can't be undone.** Deleting a lot is not held back; everything is one restore away. The guard fires only on a machine with **no system bin**, where local copies fall back to quarantine, and then only past **>25% of your files and >10 absolute**. There, one-shot `sync` stops and asks for `--confirm-deletes`; the daemon keeps syncing everything else and shows the block in `status` until you confirm. It never self-confirms. `status` and `doctor` say plainly when a machine has no bin. A large deletion that *did* run leaves a `deleted` line in `activity` with the count and where to restore from.
- **Deleting the sync folder itself is never a deletion** — it's recreated and your files downloaded back. Deleting its *contents* is a real deletion and propagates to Drive's bin.
- **`sync_dir` may be a symlink** — the folder it points to is synced; the link is left alone. Link to nothing (e.g. target's volume unmounted) → syncing stops with an error; nothing recreated, nothing deleted. `sync` and the daemon name link and target; `init` fails earlier with `create sync dir: mkdir …: file exists`.
- **Sanity guard (not confirmable).** A sync folder that is unreadable or is not a directory stops syncing with an error. So does a scan that finds nothing in a folder that is not empty. No flag overrides it.
- **Not synced** (reported by a one-shot `sync` and by `init`, not by `status`): Google-native files (Docs/Sheets/Slides), symlinks inside the folder, non-regular files, filesystem-invalid names, and Drive same-name duplicates in one folder (first wins). Ignored patterns are skipped silently.
- **A failed cycle is retried with a growing wait**, doubling from the poll interval to 10× it (45 s → 12 min at the defaults). `status` shows mode `backoff` and the error. **An explicit `sync` cuts the wait short** — fix the cause (e.g. `synckeeper login`), ask for a sync, and it applies at once. `reload` also works during the wait.
- **A repeating entry is counted, not repeated** — the same event in a row becomes one entry with a repeat count and the time it last happened, so a persistent fault can't evict your history from the 500-entry ring.
- **Crash safe.** Interrupt at any point → recovered next run; partial transfers discarded and replanned.

## 7. Recovery

| Problem | Fix |
|---|---|
| Token expired or revoked (`status`: `token: present but REJECTED`; dashboard: `credentials expired`) | `synckeeper login`, daemon running or not. Follow with `synckeeper sync` (or `s` in the dashboard) to apply it now instead of after the retry wait (§6). Recurring weekly means the client is still in **Testing** (§5). |
| **Linux:** `status` mode reads `polling-only` and the log (§4) says `out of inotify watches` | The per-user inotify budget is full — it is shared with every application, so another one may have taken it. Raise it: `sudo sysctl -w fs.inotify.max_user_watches=100000`, and persist it with `echo fs.inotify.max_user_watches=100000 \| sudo tee /etc/sysctl.d/99-synckeeper.conf`. The log names the machine's **current** value and the raise to use. Nothing is lost while you fix it: Synckeeper keeps syncing by polling and restores watching by itself — where Dropbox and Syncthing warn and stop detecting changes in real time until you act. |
| `init` says "another instance is running" | The service daemon holds the instance lock: `synckeeper service uninstall` (or Ctrl-C a `watch`), run `init`, reinstall. `login` doesn't need this. |
| Service crash-loops with "no OAuth client credentials" | Place your `credentials.json` (§5). If you've never signed in: `service uninstall` → `init` → reinstall. |
| State DB lost or corrupted | Stop the daemon, run `synckeeper init`. It can't delete: the rebuilt baseline starts empty, so every file is an upload or a download. |
| Need a deleted file back | Your system bin (a folder comes back whole), then Drive's bin, then `<config dir>/quarantine/<date>/` on a machine with no system bin. Bins expire — Drive's after ~30 days, yours possibly too. |
| Deleted a lot, want to see what went | `synckeeper activity` — a large deletion leaves a `deleted` line with the count and destination. |
| Something looks off | `synckeeper status -v`, then stop the daemon and run `synckeeper doctor`. |

## 8. Known bugs

Details in [adocs/decisions.md](adocs/decisions.md) and [adocs/audit/2026-11-11_adversarial.md](adocs/audit/2026-11-11_adversarial.md). Reproduced unless marked. **⚠ rows remove or overwrite files** — removals land in Drive's bin / the system bin, recoverable if noticed; nothing reports them as unrequested. The rest lose nothing.

| Symptom | What to do |
|---|---|
| ⚠ **`init` with the sync folder gone but `state.db` kept** (folder deleted, disk replaced, config dir restored from backup) → the whole Drive folder is trashed. | Delete `state.db` before `init` when the sync folder is missing. |
| ⚠ **`init` re-run or `doctor --repair` interrupted** (Ctrl-C, dropped connection) → next cycle moves local files to the bin as if deleted on Drive. Drive untouched. | Re-run it to completion before starting the daemon or `sync`; if too late, restore from the bin. |
| ⚠ **Two machines edit one file at once** → the later upload can replace the other edit on both machines, no conflict copy. Window: one machine's scan + transfer queue. | Other edit survives only in Drive's version history. |
| ⚠ **A tracked folder replaced by a symlink** → its Drive folder is trashed, other machines remove it. | Don't symlink inside the sync folder. |
| One unreadable file (`chmod 000`, root-owned) → nothing syncs, either direction; `status` shows a repeating `last error`. | Fix its permissions or move it out. |
| "Synckeeper" folder binned in the Drive web UI → daemon doesn't notice, keeps syncing into the binned folder (Drive purges after ~30 days). Seen against the test fake; real Drive unconfirmed. | Restore the folder from Drive's bin. |
| Linux, sync dir on a different filesystem whose mount root you can't write → a mass deletion from Drive is not held for confirmation; files go to the quarantine. From code reading, not reproduced. | Keep the sync dir on the home filesystem, or make `<mount>/.Trash-$uid` writable. |
| After a crash, nothing syncs at all — `status` shows a repeating `last error`. Happens when the crash interrupted a file being created in a Drive folder that afterwards keeps refusing to be listed (for any reason other than having been deleted). | `doctor --repair` clears the journal. |
| ⚠ A file still being written while it uploads (your edit, a large copy, a browser download — any write longer than the poll interval) → the truncated upload keeps the name on every machine; the complete file lands as the conflict copy beside it. | Nothing lost; rename the conflict copy over the truncated file. |
| Crashes around a folder rename wedge one machine: every cycle logs `plan violates §4.5: upload and download both act on …` and refuses. The refusal is the safety check working, so it repeats forever. | Edit or touch the named file — that changes the plan. Needs crashes mid-cycle; how few is unmeasured. |
| Two machines with folder names differing only in case or accents can mint a duplicate folder in Drive. | Avoid folder names that differ only in case/accents. |
| Renaming a file to differ only in case (`a.txt` → `A.txt`) makes your *other* machines retry that rename every cycle. The file is safe on both sides; the name just doesn't update elsewhere. | Change the contents too — it then converges after a transient retry. |
| **Under investigation.** A folder deleted in Drive can be *re-uploaded* by another machine instead of removed. Seen once, on a freshly adopted Linux machine; every upload failed and nothing reached Drive. Trigger not yet identified. | Check `synckeeper status` on every machine before deleting a large folder, and prefer deleting on a machine's filesystem over the Drive web UI. |

## 9. Known limitations (by design)

- One folder, one Google account, one Drive folder. No selective sync, bandwidth limits, or shared drives.
- Whole-file transfers — Drive has no delta API, so a 1-byte change re-transfers the file.
- Remote changes arrive within the poll interval (default 45 s); local changes sync under a second while the daemon runs. If file watching is unavailable — out of file descriptors, or on Linux out of inotify watches (`fs.inotify.max_user_watches`, a per-user budget; §7) — the daemon runs polling-only, everything still syncs at poll speed, and watching is restored by itself. `status` shows the mode, and the log names the cause once.
- Requires your own Google OAuth client (§5), with a one-time "unverified app" warning.
- **Full-Drive access scope.** Google offers no folder-scoped scope, and the narrower `drive.file` would silently stop syncing files added to the folder from *outside* Synckeeper (Drive web UI, other apps). So the stored token can reach your whole Drive; it's kept at `token.json` `0600` and only ever sent to Google over HTTPS. May be revisited.
- **`sync_dir` is not checked against the config dir.** Point it at a folder containing the config dir (e.g. `sync_dir = "~"` on Linux) and `token.json` / `credentials.json` upload to Drive like any other file. Keep the two apart (§4).
- **Mounting *only* the sync folder from a separate volume is unsupported.** An unmounted mountpoint looks exactly like a folder whose contents you deleted, and those deletions would propagate. (The folder itself disappearing is safe — a missing folder is recreated, never propagated.) Safe instead: a symlink to a folder *inside* the volume (`~/Synckeeper -> /mnt/data/Synckeeper`) — unmounted, the link dangles and syncing stops with an error (§6).
- **Linux syncs only while you are logged in.** The systemd user unit dies with your last session; nothing syncs until you log in again. `service install` neither enables nor offers *linger*. Want it running logged-out: `loginctl enable-linger`. macOS launchd runs whenever the machine is on.
- All files kept on disk; no online-only placeholders.
- **macOS and Linux** are validated on hardware. **Windows is not** — it also has no recycle-bin backend yet, so local rescue copies there fall back to quarantine (§6).

## 10. Live dashboard (`synckeeper status`)

On a terminal, `status` is a live dashboard: read-only by construction — no lock, no database writes, no syncing. Piped or with `--plain` you get the one-shot report instead; `--json` is unchanged for scripts.

| View | Shows |
|---|---|
| **1 overview** | next poll or retry, last sync + cycle summary, tracked/pending/quarantine counts, bin destination, recent activity — plus an **attention** block that appears only when something needs it (guard block and how to release it, expired credentials, last error, no system bin, dead daemon, missing credentials, autostart not installed) |
| **2 activity** | history, newest first, with direction and file count — the most recent 200 of the daemon's 500-entry ring (`activity -n` reads further back). Repeats collapse to one row with a count (`×288`). Scroll, filter (`f`), or search paths/details/kinds (`/`); the heading reports `N of M` |
| **3 info** | the static picture — version, paths with permission modes, sync target + Drive folder id, machine name + id, OAuth client, token status, effective config. Same data `info` prints |

| Keys | |
|---|---|
| `1` `2` `3` · `Tab` · `←` `→` | select / cycle views (`Shift-Tab` cycles back) |
| `j` `k` · `↑` `↓` | scroll the activity list |
| `PgUp` `PgDn` · `Space` · `Ctrl-F`/`Ctrl-B` | scroll a page |
| `g` `G` · `Home`/`End` | jump to newest / oldest |
| `f` | filter: all → local→drive → drive→local → conflict → errors |
| `/` | search paths, details and kinds — `Enter` keeps it, `Esc` cancels |
| `c` | clear filter and search |
| `s` | sync now |
| `p` / `P` | pause / resume automatic syncing |
| `R` | reload `config.toml` |
| `?` · `q`/`Ctrl-C`/`Esc` | help · quit |

- `s` `p` `P` `R` send the same requests as the matching commands; a result line appears under the header and clears itself. **`s` never confirms a held mass deletion** — only `synckeeper sync --confirm-deletes` does (§6).
- **A retry is named, never disguised as a poll.** While the daemon waits after a failed cycle (§6) the panel reads `retry  in 11m`, the header's `backoff` turns amber, and the attention block adds ``⚠ retrying after a failure — next try in 11m; press `s` to retry now``. `s` cuts the wait short. A paused daemon is never reported as retrying.
- **The countdown is exact when the daemon can tell you** — a running daemon publishes its real poll deadline, what a cycle is doing (`syncing  checking Drive · 45s`, `transferring · 1,145 actions · 8s`), local changes already in flight, and the live watch backend (`watching (FSEvents)`, or `polling-only` with no backend named). Falling back to the database marks the figure `≈`; the real next sync is then usually *sooner*, since any local change pre-empts the poll timer.
- **Dropped events are counted, not hidden.** When the kernel's event queue overflows (a big `git checkout` or untar inside the sync folder can outrun it) events are lost, so Synckeeper rescans at once rather than waiting for the poll. The overview then shows `overflows  3 since start — events dropped, rescanned`. The row appears only once it has happened; the count lives in the running daemon's memory, so it restarts at zero with the daemon and is not in `status --plain` or `--json`.
- **One entry, one line.** A multi-line error (a Drive auth failure carries its whole HTTP response) is flattened and cut at the window width — from the **right** for a message, so you keep the sentence; from the **left** for a path, so you keep the file name. The frame is always exactly the terminal's height.
- Colour follows `NO_COLOR` and `TERM=dumb`. `--interval` sets the state re-read period (default 1s); the frame redraws ~4×/s regardless.
