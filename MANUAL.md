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
| `status [--plain] [--json] [--interval D]` | On a terminal, the live dashboard (§10). Piped or with `--plain`, a one-shot report — daemon state, last sync, config, guard blocks, a stuck sync (§7), bin destination, recent activity; `--json` for scripts. Works daemon up or down. `--interval` sets the state re-read period (default 1s). |
| `activity [-n\|--number N]` | Last N (default 20) synced items with direction: local→drive / drive→local / conflict. |
| `pause` / `resume` | Suspend / resume automatic syncing. Explicit `sync` still works while paused. Not persisted across a daemon restart. |
| `reload` | Re-read `config.toml` in the running daemon; identity fields are reported as needing a restart (§4). |
| `config` | Print the effective config and the file it came from. |
| `account` | Which OAuth client is active and its client **id**, token status, and the signed-in Google account (needs network; skipped offline). |
| `info [--json]` | Static snapshot: version; every path with its permission mode (flagged when other users can read it); sync dir; Drive folder + id; machine name + id; OAuth client; token status; effective config; tracked / pending / quarantine counts. Read-only, offline, works before `init`. |
| `doctor [--repair]` | Cross-check state DB vs disk vs Drive. Tracked paths on disk the scan doesn't read (symlink, non-regular file, unreadable file, anything in an unreadable folder) listed as **held**, with the reason — not as missing. Names any Drive folder holding **the same name twice** — Drive allows it, a filesystem doesn't, so only the first is synced; fix it in the Drive web UI. Takes the instance lock (§7). `--repair` only ever *adds*: rebuilds lost metadata, re-adopts matching files; never deletes, quarantines, or overwrites. |
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

- **Conflicts.** Changed on both sides → remote keeps the name, your version is kept beside it as `name (conflict <machine> <date_time>).ext` and uploaded too, so every machine sees both. Nothing lost. Each upload, rename and delete re-checks the Drive file first — a folder delete also re-lists the folder's contents, one listing per folder inside; changed by another machine meanwhile → refused, and the next cycle resolves it as a conflict (or edit-beats-delete). Only a change landing between that re-check and the request completing can still be overwritten (kept in Drive's version history or bin) — for an upload that is the file's whole transfer, minutes for a large file; for a folder delete, a file edited or added in a folder after that folder was checked goes to Drive's bin with it and off the other machine (reproduced on the test fake): subfolders are checked first, deepest first, the deleted folder itself last, so the window is the rest of the check — up to minutes for a tree of thousands of folders — down to the last re-check and the delete request for the folder itself.
- **Except at `init`**, where the more recently edited side takes the plain name and the other becomes the conflict copy (renamed on Drive too). Badly skewed clocks can misname a pair at `init` — never lose one.
- **Deletes are never permanent.** Deleted in Drive → your local copy goes to **your system bin** (macOS Trash, Linux desktop trash), restorable like anything else. Deleted locally → the Drive file goes to Drive's bin (~30 days).
- **A deleted folder is one bin entry, both ways** — contents inside, restorable in one go. If something inside changed since the scan, or holds a file the sync never saw, the folder is removed file by file instead and the stranger is left alone. Deleted here while another machine edits, adds, removes or moves something inside it on Drive → the folder delete is refused that cycle (a failed `trash_remote … changed on Drive since the plan` in the log); next cycle: edited or added items come down here and stay, the rest go to Drive's bin one by one; removed or moved-out items drop out, the folder goes whole. Exception: an edit to a Google Doc/Sheet/Slides file inside is not detected — it goes to Drive's bin with the folder. Cost: one Drive listing per folder inside, 4 at a time — deleting a tree of thousands of folders holds the sync cycle for minutes.
- **No system bin? Quarantine.** Where the OS offers none (unsupported platform, a build without it, a bin that refuses the move), the copy goes to `<config dir>/quarantine/<date>/`, kept `quarantine_retention_days`. Linux, sync dir on another filesystem: the bin is that volume's `<mount>/.Trash-$uid`; can't be created or written (e.g. root-owned mount root) → quarantine. `activity` names which was used; `info` shows the destination (the home bin only — §8).
- **An edit always beats a delete.** Deleted one side, edited the other → the edit survives and comes back.
- **So does a rename on Drive.** File or folder deleted here, renamed or moved on Drive (another machine, web UI) → comes back here under the new name, a folder with Drive's current content; nothing to Drive's bin, no deletion reported. A file or subfolder deleted here *inside* a folder renamed on Drive, not renamed itself → still deleted (Drive's bin; a subfolder as one entry). Folder deleted here and a new one made here under the new name → merged into it. A file here under the new name → type clash, both left alone, reported like the rest of "Not synced" — also when that file was edited here. A file made here under the folder's *old* name → uploaded as a new file. A folder here whose name differs only in case/accents from the new name (`e` vs `E`), not holding the folder's files → same: left alone, reported. Drive now holding two folders of the new name → waits, reported. Renamed differently on two machines → both kept, two folders.
- **A file still being written while it uploads** (large copy, export, a log being appended) → Drive keeps what was on disk, under the real name; uploaded again each cycle until an upload matches the file. No conflict copy, also when both machines rename the partial file the same way before the re-upload. Meanwhile other machines may get the partial file. Each such upload is a failed action, `file changed while it uploaded`: daemon → log and `activity`; `sync` without the daemon → logged, exits with `1 of N actions failed`.
- **A folder or file Synckeeper can't read is skipped, not fatal.** The rest of the tree keeps syncing; it is left alone on **both** sides — not treated as deleted, not overwritten from Drive. Also after a plain rename (`mv`, Finder) of the file, or of its folder when another file there lets the rename be detected. Exceptions that still delete the Drive copy: §8. Reported by a one-shot `sync` and `init`, tracked ones also by `doctor` (held); the daemon logs one warning per folder or file, again only if it recurs after being fixed. Fix the permissions and it resumes by itself; unchanged content isn't re-transferred, a rename syncs as a rename. A synced file whose size and date haven't changed isn't read at all, so losing read permission alone changes nothing.
- **Renaming the Drive folder is safe** — it's tracked by Drive id. `folder_name` only names the folder if Synckeeper has to create one.
- **Moves and renames sync as moves**, files and folders alike; a folder rename travels as one operation. Exception: renaming an *empty* folder syncs as delete + recreate, costing only that folder's Drive id.
- **Same folder renamed the same way on two machines** → one folder, same Drive folder — also a subfolder renamed here inside a folder renamed elsewhere (e.g. `D/E` → `D/F` here; `D` → `N` and `E` → `F` there). A file inside edited on one machine → that edit wins; on both → conflict copy. An edit made here wins only if some non-empty file in the folder still has its synced content (proof the folder is the renamed one, not a new one of that name — an empty file proves nothing); otherwise — e.g. a one-file folder edited here — your file is kept as a conflict copy beside Drive's, one sync later.
- **A Drive folder arriving where a folder of that name is still here** — renamed onto it, or a new one, after the old folder was deleted elsewhere; or a folder made here under the name Drive renamed to → merged into one folder, Drive's id kept, never two of that name in Drive. Old files: unchanged → your bin; edited → kept, uploaded into it; new → uploaded into it. The renamed folder's old name: still holding something new (a symlink or unreadable file included) → syncs as a new folder; emptied → removed here.
- **Same file renamed the same way on two machines** → one file, no conflict copy — also when the rename replaced another file of that name on both (there an edit made here becomes a conflict copy beside Drive's). Also edited on one machine → that edit wins; edited on both → conflict copy. Exception: the editing machine synced rename and edit in one cycle (Drive gets a new file) → the other machine's old content becomes a conflict copy; nothing lost.
- **Files renamed onto each other's names elsewhere** (a chain: `f.txt` → `h.txt`, then `a.txt` → `f.txt`; a swap; a rotation) → applied here in dependency order, each file keeping its own Drive file. An unsynced edit here follows its file to the new name and goes up into that file; a file here whose name another file takes, and that is nobody's (new, never synced), is kept as a conflict copy. Renamed the same way on both sides and a new file made at the old name on both → the two new files meet like any created on both sides: same content → one file; else yours kept as a conflict copy beside Drive's. A swap passes for an instant through `name (conflict <machine> <date_time>).ext`; interrupted exactly there → completed next run; if the file was also edited here, it stays under that name and syncs as a separate file (the edit is in it); nothing lost. Renamed onto the name of a file whose sync is on hold (unreadable, behind a symlink, a Drive duplicate) → that rename waits here until the hold clears; reported like the rest of "Not synced".
- **Mass-delete guard — only where a deletion can't be undone.** Deleting a lot is not held back; everything is one restore away. The guard fires only where local copies fall back to quarantine — **no system bin**, or (Linux) none usable on the sync dir's filesystem — and then only past **>25% of your files and >10 absolute**. There, one-shot `sync` stops and asks for `--confirm-deletes`; the daemon keeps syncing everything else and shows the block in `status` until you confirm. It never self-confirms. `status` and `doctor` say plainly when a machine has no bin (not yet when only the sync dir's filesystem lacks one — §8). A large deletion that *did* run leaves a `deleted` line in `activity` with the count and where to restore from.
- **Deleting the sync folder itself is never a deletion** — it's recreated and your files downloaded back, by `sync`, the daemon or `init` (`state.db` kept or not). Deleting its *contents* is a real deletion and propagates to Drive's bin.
- **`sync_dir` may be a symlink** — the folder it points to is synced; the link is left alone. Link to nothing (e.g. target's volume unmounted) → syncing stops with an error; nothing recreated, nothing deleted. `sync`, `init` and the daemon name link and target.
- **Sanity guard (not confirmable).** A sync folder that is unreadable or is not a directory stops syncing with an error. So does a scan that finds nothing in a folder that is not empty. No flag overrides it.
- **Not synced** (reported by a one-shot `sync` and by `init`, not by `status`): Google-native files (Docs/Sheets/Slides), symlinks inside the folder, non-regular files, filesystem-invalid names, and Drive same-name duplicates in one folder (first wins). Ignored patterns are skipped silently.
- **A synced file or folder replaced by a symlink or non-regular file at the same path** (e.g. folder moved to another disk, linked back) → held, not read as deleted: left alone on both sides; Drive changes to it wait. Still held after renaming its parent folder **if** another file in that folder lets the rename be detected. A folder deleted on one side stays on the other while it holds such a link or a held file. Make it a real file/folder again → catches up; unchanged content isn't re-transferred. **Deleting the link = deleting the folder** → Drive copy to Drive's bin, removed from other machines; content behind the link untouched. Other routes that still trash it: §8. Reported like the rest of "Not synced", and by `doctor` (held); the daemon reports it nowhere yet. Never written through or over: an action that would write through a symlink inside the folder fails and is retried next cycle.
- **A failed cycle is retried with a growing wait**, doubling from the poll interval to 10× it (45 s → 12 min at the defaults). `status` shows mode `backoff` and the error. **An explicit `sync` cuts the wait short** — fix the cause (e.g. `synckeeper login`), ask for a sync, and it applies at once. `reload` also works during the wait.
- **A repeating entry is counted, not repeated** — the same event in a row becomes one entry with a repeat count and the time it last happened, so a persistent fault can't evict your history from the 500-entry ring. Exception: an action that fails *every cycle* writes a cycle summary and an error in turn, which never fold (§8).
- **Crash safe.** Interrupt at any point → recovered next run; partial transfers discarded and replanned; a rename or download already done on disk but not yet recorded is recorded first.

## 7. Recovery

| Problem | Fix |
|---|---|
| Token expired or revoked (`status`: `token: present but REJECTED`; dashboard: `credentials expired`) | `synckeeper login`, daemon running or not. Follow with `synckeeper sync` (or `s` in the dashboard) to apply it now instead of after the retry wait (§6). Recurring weekly means the client is still in **Testing** (§5). |
| **Linux:** `status` mode reads `polling-only` and the log (§4) says `out of inotify watches` | The per-user inotify budget is full — it is shared with every application, so another one may have taken it. Raise it: `sudo sysctl -w fs.inotify.max_user_watches=100000`, and persist it with `echo fs.inotify.max_user_watches=100000 \| sudo tee /etc/sysctl.d/99-synckeeper.conf`. The log names the machine's **current** value and the raise to use. Nothing is lost while you fix it: Synckeeper keeps syncing by polling and restores watching by itself — where Dropbox and Syncthing warn and stop detecting changes in real time until you act. |
| `init` says "another instance is running" | The service daemon holds the instance lock: `synckeeper service uninstall` (or Ctrl-C a `watch`), run `init`, reinstall. `login` doesn't need this. |
| Service crash-loops with "no OAuth client credentials" | Place your `credentials.json` (§5). If you've never signed in: `service uninstall` → `init` → reinstall. |
| State DB lost or corrupted | Stop the daemon, run `synckeeper init`. It can't delete: the rebuilt baseline starts empty, so every file is an upload or a download. |
| `init` or `doctor --repair` interrupted (Ctrl-C, dropped connection) | Re-run it. On an existing install the daemon / `sync` also recover alone: a half-rebuilt Drive listing is re-read in full before the next cycle plans anything. A machine's *first* `init` (or one after a lost `state.db`) interrupted → every cycle fails with ``no changes page token stored; run `synckeeper init` `` until `init` completes. Nothing is deleted either way. |
| Need a deleted file back | Your system bin (a folder comes back whole), then Drive's bin, then `<config dir>/quarantine/<date>/` on a machine with no system bin. Bins expire — Drive's after ~30 days, yours possibly too. |
| Deleted a lot, want to see what went | `synckeeper activity` — a large deletion leaves a `deleted` line with the count and destination. |
| `status` shows a repeating `last error` containing `initial walk:` after a large folder was moved or restored into the Drive folder | Listing that folder keeps failing (flaky connection, sleep). Nothing syncs until one listing completes in a single cycle; it retries every cycle from the start. Nothing is deleted meanwhile. |
| `status`: `stuck: SYNCING STOPPED for 12m, 4 cycles in a row: the plan is refused (§4.5) on "…"` (dashboard: `⚠ syncing is stuck`) | A Synckeeper bug: the safety check refuses a plan that transfers twice on one path (or on a folder and a file inside it), or that would replace a local file whose bytes are not on Drive (or bin an edit); the refusal changes nothing, so every cycle plans the same. Nothing syncs on this machine in either direction until the plan changes; no file is touched meanwhile; retrying or restarting won't clear it. Report it with that line and the daemon log (§4). Cleared by the first cycle that completes. |
| Something looks off | `synckeeper status -v`, then stop the daemon and run `synckeeper doctor`. |

## 8. Known bugs

Details in [adocs/decisions.md](adocs/decisions.md) and [adocs/audit/2026-11-11_adversarial.md](adocs/audit/2026-11-11_adversarial.md). Reproduced unless marked. **⚠ rows remove or overwrite files** — removals land in Drive's bin / the system bin, recoverable if noticed; nothing reports them as unrequested. The rest lose nothing.

| Symptom | What to do |
|---|---|
| Folder renames that differ between machines, or a folder and a subfolder renamed on Drive while only the subfolder was renamed here → one sync cycle reports a failed action (`no such file`, `disappeared`, a missing destination folder), the next one completes; nothing lost. | None needed; wait one cycle. |
| Synckeeper killed right after applying a rename or download, then `doctor --repair` run before any sync → the pending record is discarded with the journal; the old behaviour returns for that item (possible duplicate, or a plan refused every cycle). | After a crash, let one `sync` or daemon cycle run before `doctor --repair`. |
| Synckeeper killed between moving a Drive file onto a name and binning the Drive file that held that name → two files under one name in that Drive folder; one of them may go unreported. Pre-existing; from code reading. | Rename or bin the extra one in the Drive web UI. |
| ⚠ **`sync_dir` starts naming a different folder while `state.db` is kept** (config edited, its symlink retargeted, or a pre-created empty folder) → the new folder is compared to the old baseline; an empty one reads as everything deleted and the Drive folder is trashed. From code reading, not reproduced. | Changing the sync folder: stop the daemon, delete `state.db`, run `init`. |
| A folder that came down from Drive **without its contents under a build made before this fix (S018, 2026-11-11)** (moved or restored into the Drive folder while a connection dropped mid-listing) stays that way: no error, later cycles look idle, Drive untouched. Current builds retry the listing; they don't find folders left empty earlier. From code reading. | `doctor --repair` re-reads Drive in full. |
| ⚠ **A synced folder swapped for a symlink can still be trashed on Drive** and removed from other machines (content behind the link untouched here), when: (1) the link is renamed, or replaced by a differently named one; (2) the folder holding the link is renamed and nothing else in it lets the rename be detected; (3) folder and link both renamed; (4) **most likely** — the swap done in steps while the daemon runs (folder copied to the other disk, original deleted, then the link made): a local change starts a cycle after 500 ms, and a cycle in the gap reads a real deletion. (1)–(3) reproduced in the planner; (4) from code reading. | Before swapping a folder for a link: `synckeeper pause`, swap, `synckeeper resume`. Keep the link's name and its folder's name. Already trashed: restore from Drive's bin. |
| ⚠ **A file or folder Synckeeper can't read is renamed and read as deleted** → its Drive copy at the old name goes to Drive's bin, other machines remove it; the local bytes stay. When: (1) a file renamed *and* edited or touched before it became unreadable; (2) a folder renamed while holding an unreadable subfolder and nothing readable beside it. Reproduced in the planner. | Fix permissions before renaming. Already trashed: restore from Drive's bin. |
| "Synckeeper" folder binned in the Drive web UI → daemon doesn't notice, keeps syncing into the binned folder (Drive purges after ~30 days). Seen against the test fake; real Drive unconfirmed. | Restore the folder from Drive's bin. |
| Linux, sync dir on another filesystem whose `<mount>/.Trash-$uid` can't be created or written → deletions from Drive go to the quarantine and a mass deletion is held (correct since S016, 2026-11-12), but `status`, `doctor`, `info` and the daemon's start line still name the home bin as the destination. A filesystem mounted *inside* the sync dir is not probed: its subtree's mass deletion is not held when that volume's bin is unusable (files still reach the quarantine). From code reading. | Trust `activity` and a guard block in `status`. Or keep the sync dir on the home filesystem, or make `<mount>/.Trash-$uid` writable. |
| After a crash, nothing syncs at all — `status` shows a repeating `last error`. Happens when the crash interrupted a file being created in a Drive folder that afterwards keeps refusing to be listed (for any reason other than having been deleted). | `doctor --repair` clears the journal. |
| A file written without pause (a log, a live database) → one failed action every cycle while the writer runs. While any action fails each cycle: the quarantine is not purged, large-deletion warnings are not raised, `activity` records only the cycle summary and the error (no per-file entries), and those push older history out of the ring. From code reading. | Keep such files out of the sync folder: add a pattern to `ignore` (§4). |
| ⚠ A file still being written while it uploads, **and** Synckeeper is killed — or the connection drops before Drive's reply — in the instant after Drive stored it → the partial upload keeps the name on every machine; the complete file lands as the conflict copy beside it. Kill reproduced (test); lost reply from code reading. | Nothing lost; rename the conflict copy over the partial file. |
| ⚠ **Case-insensitive folder (macOS default): a folder renamed here to a name differing only in case from the name another machine gave it** (`D` → `e` here, `D` → `E` there) → Drive gets both `E` and `e`; if the other machine then adds or edits files in its folder, the machines work against each other: failed actions every cycle, and a file added there can end up only in that machine's system bin. More generally, two machines with folder names differing only in case or accents can mint a duplicate folder in Drive. Pre-existing; reproduced 2026-11-12. | Avoid folder names that differ only in case/accents. If it happened: stop the daemons, merge the two Drive folders by hand, check the system bins. |
| Case-insensitive folder: a file moved on another machine onto a name differing only in case from a new file here (`da/f0.txt` arriving, `da/F0.txt` made here) → this machine fails that move every cycle; Drive holds both names; nothing lost. | Rename one of the two files. |
| Folders swapped on Drive by id (`D` ↔ `E`), or two Drive folders given one name in one parent → a machine that changed nothing fails the same folder moves every cycle. Nothing lost. Pre-existing. | Undo the swap in two steps through a third name; give duplicate folders distinct names. |
| Renaming a file to differ only in case (`a.txt` → `A.txt`) makes your *other* machines retry that rename every cycle. The file is safe on both sides; the name just doesn't update elsewhere. | Change the contents too — it then converges after a transient retry. |
| Case-insensitive folder (macOS default) holding `Readme.txt` while Drive gains `README.txt` (from a Linux machine or the web UI) and Drive's arbitrary file-id order picks the newcomer → that machine retries downloading `README.txt` every cycle and refuses it (`a file appeared after the scan`); its `Readme.txt` stays, no longer synced. One failed action per cycle, with the side effects in the "file written without pause" row. Nothing lost. From code reading (the planned download confirmed by test); not yet run on a case-insensitive disk. | Rename one of the two in Drive (or on the case-sensitive machine) so no two names differ only in case. |
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
- **Symlinks inside the folder are never followed** — content behind one doesn't sync (§6).
- All files kept on disk; no online-only placeholders.
- **Mixed case-sensitivity.** A case-sensitive folder (Linux ext4, case-sensitive APFS) holds `Readme.txt` and `README.txt` side by side; a case-insensitive one (macOS default) can't — it keeps one, picked by Drive file id (arbitrary, not oldest), and skips the rest as a case-collision (reported like "Not synced", §6). Drive and case-sensitive machines keep both. When the one picked isn't the one already on disk: known bug, §8.
- **macOS and Linux** are validated on hardware — Linux by S001's gates on inotify (2026-11-12: full suite incl. `-race`, 50k files with 0 of 500 folders unwatchable, 2 h simulated two-machine soak) plus field use since 2026-07. **Windows is not** — it also has no recycle-bin backend yet, so local rescue copies there fall back to quarantine (§6).

## 10. Live dashboard (`synckeeper status`)

On a terminal, `status` is a live dashboard: read-only by construction — no lock, no database writes, no syncing. Piped or with `--plain` you get the one-shot report instead; `--json` is unchanged for scripts.

| View | Shows |
|---|---|
| **1 overview** | next poll or retry, last sync + cycle summary, tracked/pending/quarantine counts, bin destination, recent activity — plus an **attention** block that appears only when something needs it (guard block and how to release it, expired credentials, syncing stuck, last error, no system bin, dead daemon, missing credentials, autostart not installed) |
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
- **A stuck sync is named, not left to a repeating error.** When the safety check refuses the daemon's plan (§7) two cycles running, the attention block adds `⚠ syncing is stuck — for 12m, 4 cycles in a row: the plan is refused (§4.5) on "N/f.txt"; nothing syncs in either direction` above the `last error`, and a hint line. Same fact in `status --plain` (`stuck:` line) and `--json` (`daemon.standstill`: `since`, `cycles`, `paths`, `reason`; `null` when not stuck). Kept in the daemon's memory: cleared by the first cycle that completes; after a daemon restart, back after two cycles.
- **One entry, one line.** A multi-line error (a Drive auth failure carries its whole HTTP response) is flattened and cut at the window width — from the **right** for a message, so you keep the sentence; from the **left** for a path, so you keep the file name. The frame is always exactly the terminal's height.
- Colour follows `NO_COLOR` and `TERM=dumb`. `--interval` sets the state re-read period (default 1s); the frame redraws ~4×/s regardless.
