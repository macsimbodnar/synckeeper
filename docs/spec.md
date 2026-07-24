# Synckeeper — Design Document & Implementation Contract

This is the authoritative specification: scope, detailed behavior, and acceptance criteria. The execution plan in [plan.md](plan.md) breaks it into work; when the two disagree, this file wins. Changes to scope or invariants are recorded in [decisions.md](decisions.md).

Revised 2026-07-17: rewritten platform-agnostically (behavior is defined by filesystem/OS *capabilities*, never by OS name), daemon-first, with an explicit OS-integration module contract and a Dropbox reference appendix. Platform order lives only in the [roadmap](#roadmap); it is not part of the feature definition.

## 1. What Synckeeper is

A personal bidirectional file sync tool written in Go. It keeps one designated local folder identical to one designated folder on a Google Drive account. Multiple desktop machines each sync independently against Drive, which acts as the hub and the durable copy. Everything inside the folder is synced recursively; nothing outside it is ever touched. Files live as normal files on disk.

**The daemon is the product.** The primary mode of operation is a continuously running background daemon that watches both sides and keeps them converged; the CLI (and any future UI) are thin clients of it. A standalone one-shot `sync` exists as a fallback and scripting tool, and shares the exact same sync path — there is deliberately no second sync implementation anywhere.

Built for a single user, so distribution polish and Google verification are skipped. Data-integrity guarantees are NOT skipped: the engine must never silently lose or corrupt a file, even across crashes, offline edits on multiple machines, or a lost state database.

## 2. Scope

**In scope (v1):** bidirectional sync of regular files and directories; conflict preservation; move/rename preservation; trash/quarantine instead of deletion; multi-machine convergence via `--adopt`; continuous daemon with OS-native change detection; monitoring (`status`, `activity`) and control (`sync`, `pause`, `resume`, `reload`) of the running daemon; `doctor` self-check and additive repair; login-service autostart.

**Someday (explicitly deferred, recorded so they shape interfaces but not schedules):** per-item ignore markers (Dropbox-style attribute), path-based ignore patterns, online-only placeholder files, pluggable storage backends (which would unlock block-level delta sync), account keyring storage, tray UI and file-manager badges (see [§10](#10-os-integration-modules)).

**Out of scope (rejected, with reasons):** selective sync (single-user tool, one folder); bandwidth limits; shared drives; multiple sync folders or accounts; file-version browsing (Drive's own revision history is the backstop); mobile; push webhooks (needs a public HTTPS endpoint).

## 3. Non-negotiable durability invariants

1. Three-way reconcile against a persistent local baseline DB. Never diff local vs remote directly. (The baseline is the *merge base*: it is what lets the engine distinguish "changed here" from "deleted there".)
2. Atomic local writes: download to a temp file in the destination directory, verify checksum, fsync, atomic rename, then commit the DB row. Never commit the DB before the data is durable.
3. Deletes go to Drive trash (remote) and a dated local quarantine directory (local), never permanent. The quarantine's rescue copies are themselves protected: a colliding destination uniquifies rather than overwrites, and a quarantined directory carries its invisible leftovers (ignored and temp files) with it instead of wedging on them. *(Sharpened 2026-07-18, W1.9 — both failure shapes were reproduced: a same-day re-quarantine silently destroyed the earlier rescue copy, and a `.DS_Store` made a folder unquarantinable forever. See decisions.md. Leftover-carry implemented 2026-07-19, W1.9.2 — R20: ignored-or-temp entries sweep into the dated quarantine destination before the empty-dir removal; anything else still refuses. Rescue-copy protection implemented 2026-07-19, W1.9.3 — R21: the destination uniquifies with a numbered suffix (`f (2).txt`), and the guarded move refuses any occupied destination beneath it.)*
4. Mass-delete guard: refuse to propagate deletion of more than a configurable fraction of tracked files without explicit confirmation. Treat "sync dir missing/unreadable/empty-with-nonempty-baseline" as an error, never as "everything deleted".
5. Conflicts produce a conflicted copy with machine name and timestamp. Never last-writer-wins, never overwrite.
6. Every operation idempotent and crash-resumable. Killing the process at any point leaves a state a fresh run repairs.
7. **Plan ordering is dependency-aware, and destruction never outruns protection.** An action that overwrites, moves away, or removes content must be ordered after every action that preserves that content or vacates/creates the paths it relies on — and must not execute at all if a protective action it depends on failed. Lexicographic or stage ordering alone is not sufficient. *(Added 2026-07-17 after review found two ordering bugs, one losing data — see decisions.md.)*

## 4. Sync semantics

### 4.1 The three trees

Every sync cycle builds three views keyed by `rel_path` (posix-style, relative to the sync root):

- **base** — the baseline DB: state as of the last completed sync (file id, size, content md5, local mtime, remote md5/version).
- **local** — a scan of the sync dir (md5 computed only when size or mtime differ from base; the baseline hash is trusted otherwise).
- **remote** — derived from a locally cached mirror of Drive metadata, maintained incrementally from the `changes.list` feed and pruned to the synced tree.

Reconciliation is a **pure function** `(base, local, remote) → ordered action plan`. No I/O, no clock beyond an injected timestamp, fully unit-testable. This is the most-tested code in the project.

### 4.2 Decision table (per item)

`local_changed` = local differs from base (md5); `remote_changed` = remote differs from base (md5/version).

| base | local | remote | action |
|---|---|---|---|
| absent | new | absent | upload, insert row |
| absent | absent | new | download, insert row |
| absent | new | new, same md5 | adopt: record row, no transfer |
| absent | new | new, diff md5 | conflict: local becomes the conflicted copy, download remote |
| present | unchanged | unchanged | nothing (refresh row if metadata drifted) |
| present | changed | unchanged | upload new revision |
| present | unchanged | changed | download (atomic replace) |
| present | changed | changed, same md5 | record, no transfer |
| present | changed | changed, diff md5 | conflict: local becomes the conflicted copy, remote wins the canonical name |
| present | deleted | unchanged | trash remote, delete row (guarded) |
| present | unchanged | trashed/deleted | quarantine local file, delete row |
| present | changed | trashed | resurrect: re-upload local (edit beats delete) |
| present | deleted | changed **or moved** | download remote (edit beats delete) |
| present | deleted | trashed/deleted | forget row |

**Edit beats delete, always, in both directions.** A resurrected file is a nuisance; a lost edit is data loss.

**A type clash — a file on one side, a directory on the other, at one rel_path — is reported and left alone, in every pass and both directions:** no transfer, no delete, and no creation planned beside it. *(Generalized 2026-07-18, W1.9: the rule existed only for baseline rows; the new-item passes error-looped on the clash and blind-uploaded a same-name file next to the remote folder. See decisions.md. Implemented 2026-07-19, W1.9.4 — R22: passes 2 and 3 skip-and-claim on a mismatch; when the clashing occupant is itself resolved by the plan's delete side, the remote item materializes cleanly one cycle later.)*

**Every row is decided at the path the item will occupy when the action runs.** When an ancestor directory moved remotely in the same cycle, a local path's remote counterpart must be looked up at its *post-move* path; resolving it at the pre-move path silently skips the row that applies (typically `absent | new | new, diff md5` → conflict) and can emit two actions for one path. *(Adopted 2026-07-18, W1.8 — see decisions.md. Implemented 2026-07-18, W1.8.4 — R11; the conflict backup acts at the post-move path, where the ancestor's hoisted MoveLocal has already put the local content.)*

**Remote wins the canonical name in conflicts** — deterministic across machines (they all agree on what Drive holds); the local version is preserved as the conflicted copy *and uploaded*, so every machine sees both versions. Conflict copy naming: `<stem> (conflict <machine_name> <YYYY-MM-DD_HHMMSS>)<suffix>`.

### 4.3 Moves and renames

Moves are preserved as moves — never delete + re-transfer:

- **Remote-driven:** same file id at a new name/parent inside the tree → local rename/move. Moved out of the tree → remote delete. Moved into the tree → remote create (with a one-time subtree walk, since the changes feed does not enumerate descendants of a moved folder).
- **Local-driven:** the scanner sees delete + create; before executing, deletions and creations with identical md5+size are paired into a remote move (rename/reparent by id). Unpaired remainders stay delete + create. A case-only rename on a case-folding filesystem is therefore a move, not a trash + upload.
- **Pairing is bidirectional and covers directories.** A directory has no content hash to pair on, and a newly appeared local directory has no file id yet — so a locally renamed directory is paired from **the evidence of its children**. When a baseline directory is gone locally, its remote is unchanged (the same precondition file pairing uses), and every *surviving* tracked descendant paired into one new location preserving its relative subpath, then the directory itself is the move: it becomes **one** remote rename/reparent, and the per-file moves it explains are dropped rather than issued, because reparenting a Drive folder carries its whole subtree. Descendants that were genuinely deleted do not block the pairing; a descendant that paired somewhere *else* does — that is a scatter, not a rename. A directory with no surviving children pairs to nothing and stays delete + create: there is no evidence, an empty directory has no content to preserve, and inventing a move from ambiguity risks reparenting a subtree the user never touched. **The asymmetry is deliberate — a missed pairing costs a churned folder id, a wrong pairing moves data the plan never reasoned about.** A folder rename must never plan a remote folder create + trash: that churns the Drive id, discards the folder's Drive-side history and sharing, and reaches every other machine as a delete. *(Adopted 2026-07-18, W1.8: this direction was previously unstated, and the code took the lossy reading — a plain local folder rename planned mkdir + per-file moves + trash. See decisions.md.)* **Once the collapse pairs D → N, remote-side rows under D resolve at their post-reparent paths.** Children another machine added or edited concurrently — rows the pairing did not explain — are planned at their location under N, because the reparent carries them there: a remote-only creation downloads into N, an edit-beats-delete download lands in N, and the plan never scaffolds D locally after deciding D moved. Both directions share one rule — every row is decided at the path the item will occupy after the planned directory moves execute, applied to *whichever side has not moved yet* (§4.2's post-move rule, made symmetric). *(Adopted 2026-07-18, plan review: the earlier text left this edge to "check §4.4", and the naive reading plans a zombie D/ download beside the reparent — the identity churn A1 exists to remove. See decisions.md. Implemented 2026-07-18, W1.8.2 — R9; two conservative guards added in the decided rule's spirit: the destination must be a brand-new local dir, and its name must be free remotely.)*
- A remote directory move is applied as **one** local directory rename; descendants' path changes are explained by it rather than fanned out into per-file moves.

### 4.4 Directories

Created before children (top-down), removed only when empty after children resolved (bottom-up). Delete-folder vs add-file-inside resolves as resurrect-the-folder. A locally deleted folder whose remote gained new content survives; the new content syncs down.

### 4.5 Plan ordering (normative — invariant 7)

Stage order *(refined 2026-07-17 with the R2 fix; the local-driven reparent slotted 2026-07-18, W1.8.2)*: **local** directory moves (top-down) → mkdirs (top-down) → **remote directory reparents** (top-down — a local-driven rename's one move) → remote mkdirs targeting a just-reparented directory → file moves and conflict backups → transfers → deletes (bottom-up, files before their parent dirs). Local directory moves precede all creations because a creation may scaffold a move's destination; a reparent follows the mkdirs because its destination parent may be a folder being created, and precedes the file moves and remote mkdirs that resolve parent ids through it — the same dependency rule throughout (invariant 7). Within and across stages the following dependencies are binding:

- A **conflict backup** must run against the path where the local content *currently* is — i.e. it must be sequenced before (or expressed independently of) any local move that relocates that content. A download onto a canonical path must not run if the conflict backup protecting that path's previous content failed.
- A **local directory creation** (`MkdirAll` included) must not create the destination path of a pending local move; moves into or out of a path order before creations of that path.
- A **local move or conflict backup must never overwrite a file the plan did not account for** *(added 2026-07-17, R7/R8)*. A remote move whose destination is already occupied locally resolves by occupant: tracked content (its bytes are on Drive) may be clobbered; **untracked local-only content is preserved as a conflict copy** (backed up before the move, then uploaded), with the move `ProtectedBy` that backup. Occupant-preserving backups order before the moves that reclaim their paths. At execution both `MoveLocal` and `ConflictBackup` re-stat the destination immediately before the rename and refuse on any unexpected occupant — the same overwrite guard downloads carry (§7); a racing local write wins the cycle and reconciles next.
- Actions touching the same rel_path, or an ancestor/descendant pair, never run concurrently. Transfers may otherwise run in a small worker pool; plan generation and DB commits are serialized. **This rule is enforced, not assumed:** the transfer stage — the only stage executed concurrently — is checked for overlap (same rel_path, or an ancestor/descendant pair) before it executes, so a planner mistake surfaces as a refused plan rather than as a race. Serial stages are exempt by design: their *ordering* is the mechanism — nested mkdirs, bottom-up deletes, and an occupant-preserving backup followed by the move that reclaims its path (R7) are all correct same-stage sequences on overlapping paths. *(Enforcement adopted 2026-07-18, W1.8 — the rule was normative but unimplemented, and a reconcile bug did emit an upload and a download for one rel_path into the same parallel stage. Scoped to the concurrent stage later the same day: the blanket "same-stage" wording would have refused those correct serial sequences. See decisions.md. Implemented 2026-07-19, W1.8.5 — R12, `reconcile.ValidateTransferStage`; a directory Record commits a DB row only and is exempt from the ancestor rule, not the same-path one.)*

### 4.6 Crash resume

Every action is journaled (`pending_ops`) before execution, marked in-progress when started, and marked done in the same transaction that commits its baseline row. Recovery is **discard-and-replan**: stale ops are dropped and orphan temp files removed at cycle start; the fresh plan self-heals partial effects (e.g. an uploaded-but-uncommitted file reappears as both-new-same-md5 → adopt). **A move whose disk/Drive half committed but whose baseline row did not** — a crashed `MoveRemote`/`MoveLocal`, or the user making the same rename the remote already reflects — is a symmetric self-heal: the baseline file is absent at its old path while the same file id sits at the new path on *both* the remote and locally, so the row is recorded in place at the new path (drop the stale old-path row) rather than read as "locally deleted + remote-moved," which would plan a download of the restored file and an upload of the "new" local file onto one rel_path and wedge on the §4.5 refusal every cycle. *(Added 2026-07-23, W4/R25 — found by the FZ1 fuzzer; reproduced with and without a crash. See decisions.md.)*

## 5. Name mapping, collisions, and skips

rel_paths are posix-style, never absolute, no `.`/`..` segments. Behavior adapts to **probed filesystem capabilities**, not OS identity:

- **Case folding** (probed by creating a temp file and statting a case-toggled name): when the local FS folds case, remote siblings differing only by case collapse — first by file id wins, the rest are skipped and reported. Nothing is ever silently clobbered.
- **Unicode normalization folding** (probed the same way with an NFC/NFD-toggled name): when the local FS is normalization-insensitive, remote siblings differing only by normalization collapse identically. Names are compared under case+normalization fold wherever the local FS folds them. *(Adopted 2026-07-17; Dropbox normalizes server-side to NFC — we fold at the mapping layer instead, since we do not control the server. Implemented 2026-07-18: `names.NormalizationInsensitiveFS` probe + `names.FoldKey` combined case/normalization fold in `remotedelta.Snapshot`; W2.2.)* **The comparison applies across trees, not only among remote siblings:** a new local name fold-equal to a new remote name resolves through the ordinary §4.2 rows — adopt or conflict, remote winning the canonical byte-form — never a blind upload; and a baseline row whose remote was fold-skipped from the snapshot is held harmless as a reported skip, never treated as remote-deleted. *(Made explicit 2026-07-18, W1.9 — reproduced: a fold-blind upload minted a permanent Drive case-duplicate and the engine then quarantined the user's local file. See decisions.md. Implemented 2026-07-19, W1.9.1 — R19: fold-keyed sibling index in reconcile pass 2 for files, the adopt landing via a case-only rename to the remote byte form; shadowed ids — fold-skipped **and** duplicate-name-skipped — held harmless in passes 1 and 4. The **directory arm is a recorded follow-up**: a new local folder fold-equal to a remote folder still mints a duplicate folder; closing it needs the case-only-rename arm of the §7 gate for directories. See decisions.md.)* **The held-harmless guarantee covers a shadowed folder's whole subtree, not just the directly-colliding row:** a folder that loses "first by id" is skipped without its subtree being walked, so its tracked descendants are held harmless too, never read as remote-deleted. *(Added 2026-07-21, W1.9.6 — reproduced: a shadowed folder's tracked child was quarantined off disk because only the folder's own id was held harmless. See decisions.md. Implemented — R24: the engine drags every baseline row under a shadowed skip's rel_path into the harmless set.)*
- **Duplicate names** in one Drive folder (Drive allows them): first by file id wins; the rest are skipped and reported.
- **Invalid names** (empty, `.`, `..`, path separators, NUL; per-platform rules added by the OS module): skipped and reported, never transliterated.
- **Skipped, always reported, never silent:** Google-native files (Docs/Sheets/Slides), symlinks, non-regular files, invalid names, and name collisions. **Ignore-pattern matches are the one deliberate exception — silent by design:** the user chose the patterns, and re-reporting every `.DS_Store` each cycle is noise, not signal. *(Narrowed 2026-07-18, W1.9/C7 — the code never reported them; the promise was wrong, not the behavior. See decisions.md.)*
- **Ignore patterns (v1):** glob patterns matched against the base name only (defaults: `*.tmp`, `~$*`, `.DS_Store`, `Thumbs.db`, `*.swp`, `.synckeeper*`). Path-based patterns and per-item markers are *someday* items.

## 6. Guards

- **Mass delete:** a plan that trashes/quarantines more than `mass_delete_threshold` of tracked **files** (and more than 10 absolute) requires `--confirm-deletes`. The guard counts content, not containers: directory deletions are excluded from both the count and the denominator, since an empty folder disappearing is not the loss the guard exists to catch. *(Narrowed 2026-07-18, W1.8: counting directories made an ordinary folder rename trip the guard — aborting the one-shot and wedging the daemon in a permanent block — which trains exactly the reflexive `--confirm-deletes` habit the guard depends on the user *not* having. See decisions.md. Implemented 2026-07-18, W1.8.3 — R10/G4; with A1 landed, the live trigger was the empty-folder-tree rename, which legitimately syncs as delete + create.)* The **interactive one-shot `sync` aborts the whole cycle** with the report and hint. The **daemon never self-confirms and never blocks the whole cycle**: it strips only the delete-class actions, executes everything else, logs loudly, and surfaces the block in `status` until the human confirms. *(Split clarified 2026-07-17: the daemon previously aborted the entire cycle, contradicting "keeps syncing everything else"; now it defers only the deletes.)*
- **Sync dir sanity:** missing, unreadable, not-a-directory, or empty-while-baseline-nonempty → hard error, no plan executed.
- **Single instance:** file lock in the config dir. Read-only commands never take it.

## 7. Transfer protocol

**The local-write gate (normative).** Every primitive that mutates a local path — download's atomic replace, `MoveLocal`, `ConflictBackup`, `QuarantineLocal`, and `Record` (which overwrites the baseline's *truth*, and so counts as destruction under invariant 7) — states what it expects to find there and is refused if reality differs: the path is re-statted immediately before the mutation, and any drift (changed stat, appearance where absence was assumed, disappearance where presence was assumed, non-regular file) abandons the action and replans next cycle. A racing local write always wins the cycle. This is **one shared choke point, not a per-primitive convention**: there is a single place in the executor permitted to touch a local path, every primitive routes through it, and a test enforces that no other code in the package calls a raw filesystem-mutating function. *(Adopted 2026-07-18, W1.8. The check had been added four times — R4 downloads, R6 records, R7 moves, R8 backups — each time at one more call site, and `QuarantineLocal` was still unguarded, so an edit landing between scan and execution was quarantined instead of winning the cycle: recoverable bytes, but "edit beats delete" broken silently. A discipline that four fixes failed to make stick belongs in the type system, not in review. See decisions.md. Implemented 2026-07-18, W1.8.1: `internal/executor/localwrite.go` + the AST gate test; quarantine now stat-pinned and refused on drift — R13.)* One recorded exemption: `doctor --repair` removes orphan `.synckeeper.tmp.*` files (its own, prefix-matched) from outside the executor package; it stays outside the gate deliberately — additive repair runs without a plan to state expectations against — and is named here rather than left discoverable. *(Recorded 2026-07-18, plan review.)* **The commit of a remote move is a Record in disguise and obeys the same rule:** it must never restate local truth it did not verify — the renamed row keeps its scanned stat and md5, and only the Drive-side fields are refreshed, with no stat of the destination at all. *(Added 2026-07-18, W1.8.2/R18 — the commit stamped the scanned md5 onto a mid-cycle edit's stat; reproduced as silent permanent divergence, the R6 class at a sixth commit site. See decisions.md.)*

- **Downloads:** stream to `.synckeeper.tmp.<random>` in the destination directory, md5 computed on the fly and verified against Drive's checksum, fsync, atomic rename onto the target, fsync parent dir, then commit. **Overwrite guard:** immediately before the rename, the target is re-statted; if size/mtime no longer match what the cycle's scan observed, the download is abandoned and requeued (the local edit wins this round and reconciles next cycle). *(Added 2026-07-17.)* The same guard applies to the two other rename primitives that land on a local path — **`MoveLocal` and `ConflictBackup`** re-stat their destination and refuse an unexpected occupant (§4.5). *(Extended 2026-07-17, R7/R8.)*
- **Uploads:** hash before upload; chunked resumable media (8 MB chunks) above 5 MB, simple upload otherwise. Commit only after Drive returns metadata whose checksum matches the pre-upload hash; if the file changed mid-upload, commit nothing new — the next scan sees it dirty and re-uploads.
- **Retries:** exponential backoff with jitter on rate-limit/quota and 5xx errors, max 5 attempts; hard failures stay journaled for the next cycle.

## 8. The daemon

### 8.1 Sync loop

One serialized loop owns the engine. Triggers: (a) coalesced local-change hints from the watcher, debounced (default 500 ms); (b) a remote polling tick (`poll_interval_secs`, default 45 s); (c) an explicit `sync` command. **Every trigger runs the same full cycle** (full local scan + remote delta). Change hints only ever *wake* the loop — they never carry truth, so lost or duplicated events are harmless and dropped events are recovered within one poll interval. Cycle failures back off exponentially (capped at 10× the poll interval) without exiting; guard blocks wait loudly for the human.

### 8.2 Monitoring (no IPC required)

The daemon records to its DB: a heartbeat (10 s) from a dedicated goroutine so long cycles can't fake death; per-action **activity** entries (capped ring) labeled with direction — `local→drive`, `drive→local`, `conflict`; the last cycle summary, next poll estimate, and any guard block. A failed cycle records an error entry, never per-action success claims. `status`/`activity` read this without the lock and work when the daemon is down. Liveness: control-socket ping (authoritative) → else heartbeat freshness → else recorded state.

### 8.3 Control (local socket)

A local control socket (filesystem permissions are the whole access model; never a TCP port) speaks line-delimited JSON with a protocol-version handshake. Commands: `ping`, `sync [confirm_deletes]`, `pause`, `resume`, `reload`. Mutating commands hand work to the sync loop via channels; the loop remains sole owner of engine and config. `pause` suspends automatic triggers only (explicit `sync` still runs; pause state is in-memory and clears on restart). `reload` applies hot fields (poll interval, ignore globs, thresholds, retention) live and reports identity/path fields as needing a restart. Hot fields are **published safely**: the sync loop owns config, but the watcher's event pump reads the ignore globs on every event, so a live swap must be synchronized rather than written in place. *(Clarified 2026-07-18, W1.8 — a confirmed data race; "the loop is sole owner" held for sync cycles and not for the watcher goroutine. See decisions.md. Implemented 2026-07-19, W1.8.6 — R14: the loop publishes an atomic ignore-glob snapshot the pump loads per event; the other hot fields, poll cadence included, are verified loop-only and stay plain writes.)* If the socket can't be created the daemon logs and runs on — control is a convenience, never a dependency.

### 8.4 CLI-daemon interplay

When the daemon runs, `sync` delegates over the socket and waits by watching recorded status; `sync --dry-run` requires a standalone run and says so. `service install|uninstall|status` manages the login service through the OS module. Re-authentication (`login`) takes the instance lock — which usefully forces the daemon to be stopped first, since it holds the old token in memory.

## 9. Storage backend: Google Drive

- OAuth Desktop-app client; full `drive` scope; consent screen published to Production but unverified (Testing status expires refresh tokens in 7 days, which kills a daemon).
- **Credential distribution model** *(decided 2026-07-17)*: the app ships with the author's client id/secret embedded as the working default — a desktop client secret is not confidential by design, and this gives zero-setup onboarding. Users who want dedicated API quota ("more speed") can supply their **own** client credentials, which take precedence over the embedded ones (lookup order: `credentials.json` in the config dir → embedded). *(Implemented 2026-07-18, W2.4. The tertiary "config keys" tier was dropped: config-key credentials would be printed by `synckeeper config`, and `credentials.json` already accepts the exact JSON the Cloud Console downloads — one file, zero transcription. See decisions.md.)* Consequences accepted: all default-credential users share the author's per-project quota (per-user rate limits still isolate them from each other); the unverified full-drive scope caps the default client at ~100 users and shows a consent warning. Growth path if the cap is ever reached: Google verification (funded by donations) or the BYO instructions. API calls themselves are free — quota is rate-limited, not billed.
- OAuth loopback flow with **S256 PKCE** — the client secret is public by design, so code-interception protection is load-bearing here, not ceremony *(adopted 2026-07-18, W1.9; implemented 2026-07-19, W1.9.5 — R23 drives the whole loopback flow against a fake token endpoint and verifies S256(verifier) equals the URL's promised challenge)*; token JSON at 0600, atomically replaced, auto-refreshed and re-persisted.
- Remote change detection by polling `changes.list` with a persisted page token, applied to the local metadata mirror; the token is persisted only after its batch is fully applied. Initial state via a full recursive walk.
- Trash via metadata update, never permanent delete. Skip `application/vnd.google-apps.*` (except folders).
- **Accepted limitations imposed by the backend:** whole-file transfers only (no block-level delta, no LAN/peer sync, no streaming-while-uploading), no push notifications. Revisit only behind a pluggable-backend design.

## 10. OS integration modules

Functionality is agnostic; implementations are modular per OS and **should exploit the OS's native APIs** for robustness and efficiency. Each module is a small interface with per-OS files; porting to the next OS means implementing the modules, nothing else. Everything above this section must not mention or depend on a specific OS.

| Module | Contract | Native implementations (in rollout order) |
|---|---|---|
| **fswatch** | Register a root; receive coalesced change hints (best-effort, may drop/duplicate — the sync loop treats them as wake-ups only). Report degradation; the daemon falls back to pure polling and periodically retries. *(Interface extracted 2026-07-23, W3.1: `fsWatcher` in `internal/watch/fswatch.go` — `refresh(root) → failed-count` for registration + fd-pressure, a `wake` callback for hints, `close`; the fsnotify backend is the first implementation. **FSEvents implemented 2026-07-23, W3.2** — `internal/watch/fsevents_darwin.go` (`//go:build darwin && cgo`), the repo's first cgo: one recursive file-level stream on a serial dispatch queue, `refresh` a near-no-op returning 0 (new dirs auto-covered, zero per-file fds) whose one job is liveness — it re-stats root and returns 1 when the device id changed (a volume remount, e.g. an external-drive sync dir) or root vanished, so the failure latch degrades to polling and the recovery path recreates the stream on the current volume (parity with fsnotify's per-cycle re-walk) *(added 2026-07-24, W3 follow-up)*, ignored paths filtered before waking *(component-wise since 2026-07-23, W3 adversarial check: churn under an ignored directory never wakes — parity with the scanner's subtree skip and with fsnotify, which never watches inside ignored dirs)*. Selected on darwin+cgo via an `init` override of `newBackend`; the pure-Go fsnotify backend stays the fallback when cgo is off and on every other platform. Tests pin fsnotify (TestMain) so R14/R15 stay meaningful; a darwin+cgo test covers the FSEvents wake-up.)* | FSEvents (macOS; directory-tree stream, zero per-file descriptors); inotify (Linux, per-directory watches); ReadDirectoryChangesW (Windows, one recursive handle). Universal fallback: poll-only. |
| **fsprobe** | Capability probes of the sync dir: case folding, normalization folding. Safe default: no folding (never collapse distinct names). | Shared probe-by-creation implementation; per-OS only if needed. |
| **names** | Per-platform validity rules layered on the common ones. | Reserved device names, illegal characters, length limits added per OS as its port lands. |
| **service** | Install/uninstall/status of the login service running `watch`. | launchd agent; systemd user unit; Task Scheduler. |
| **ui (future)** | Separate binary; strictly a client of the control socket and status DB; holds no sync logic, cannot threaten data integrity. | Menu-bar/tray app; file-manager badge extensions where the OS offers an API. |

**Build policy** *(changed 2026-07-17; first cgo landed 2026-07-23, W3.2)*: binaries are built natively on each target platform; cross-compilation is not a requirement. cgo is permitted — required for FSEvents — with pure Go preferred where it is equally good (the SQLite driver stays pure-Go `modernc.org/sqlite`; no reason to churn). The FSEvents backend is the first and only cgo, confined to `//go:build darwin && cgo`; `make build`/`test`/`vet` run natively with cgo at its platform default (on → FSEvents on macOS), while `make build-all` cross-compiles pure-Go (`CGO_ENABLED=0`), which drops FSEvents to the fsnotify fallback. The fd-exhaustion mitigations (rlimit raise, watcher rebuild, polling latch) remain as belt-and-braces around whatever backend runs — the periodic watcher rebuild is now per-backend (`fsWatcher.needsRebuild()`: fsnotify yes, FSEvents no, since a directory-tree stream holds no per-file descriptors to leak; W3.4, 2026-07-23), while the rlimit raise and the polling latch stay for every backend.

## 11. Multi-machine

A new machine joins with `init --adopt`: adoption is the ordinary first sync over an **empty baseline**, which structurally cannot produce a delete (delete-class actions require a baseline row missing on one side). Union merge: local-only uploads, remote-only downloads, md5-equal pairs adopt, divergent pairs conflict. Plain `init` on a non-empty Drive folder refuses and points at `--adopt`, persisting nothing so the retry is clean. Machine identity: a random persisted `machine_id` plus a human `machine_name` used in conflict filenames.

## 12. Repair

`doctor` cross-checks DB vs disk vs Drive and reports divergence. `doctor --repair` **only ever adds**: restores metadata (folder id by name, machine id, fresh page token), force-rebuilds the remote mirror, adopts md5-equal pairs into the baseline, clears stale journal rows and orphan temps. It never trashes, quarantines, or overwrites; after a lost DB, one-sided files become plain uploads/downloads on the next sync. `init --force` (re-init over an existing DB) must leave the remote mirror coherent — it performs the same forced full walk as repair. *(Clarified 2026-07-17; previously it silently reset the page token over a stale mirror.)*

## 13. Configuration

TOML in the per-OS config dir (`os.UserConfigDir()`-based); state DB, token, quarantine, lock, and control socket live alongside it.

```toml
[drive]
folder_name = "Synckeeper"      # created at Drive root if absent on first init

[local]
sync_dir = "~/Synckeeper"

[engine]
poll_interval_secs = 45          # remote poll cadence; also the full-rescan cadence (hot-reloadable)
mass_delete_threshold = 0.25     # fraction of tracked files (hot)
machine_name = "max_mbp"         # conflict filenames; sanitized (restart)
quarantine_retention_days = 30   # purge after a fully successful sync (hot)
ignore = ["*.tmp", "~$*", ".DS_Store", "Thumbs.db", "*.swp", ".synckeeper*"]  # (hot)
```

`full_rescan_interval_secs` is **removed** *(2026-07-17)*: every cycle is a full rescan by design, so the knob was dead. Unknown keys are rejected to catch typos; missing keys fall back to defaults.

## 14. State DB

SQLite via `database/sql`, WAL, single connection, writes serialized. Versioned migrations; a binary refuses a schema newer than it knows, and **read-only commands must not migrate** — migrations run only under the instance lock *(2026-07-17)*. Tables: `items` (baseline; keyed by file id, unique rel_path), `meta` (page token, root folder id, machine id, schema version), `pending_ops` (journal), `remote_nodes` (Drive metadata mirror), `daemon_status` (heartbeat singleton), `activity` (capped ring with direction).

## 15. CLI surface

`init [--adopt|--force]` · `login` · `sync [--dry-run] [--confirm-deletes]` · `watch` · `status [--json] [--watch]` · `activity [-n]` · `pause` / `resume` / `reload` · `config` · `account` · `doctor [--repair]` · `service install|uninstall|status`. Human output first; `--json` where a future UI needs it.

## 16. Acceptance criteria

The test matrix in [testing.md](testing.md) is the ledger; a feature is done when its rows pass. Summary of what must hold:

1. **Reconcile:** every decision-table row covered by table-driven tests; move pairing **in both directions, files and directories** (R9); directory ordering; dependency ordering of §4.5 (regression tests R1, R2); move/backup destination-overwrite guard (R7, R8); rows resolved at post-move paths (R11); no rel_path overlap in the concurrent transfer stage (R12); type clashes are reported skips in every pass (R22).
2. **Scenarios S1–S8** on the fake backend: create/edit/delete/rename each direction, conflicts preserved on all machines, edit-beats-delete both orders, deep trees.
3. **Faults F1–F5:** crash at every checkpoint then rerun converges; lost DB repaired additively; unmounted dir is a hard error.
4. **Guards G1–G3** block without confirmation and surface in `status`; the daemon defers the deletes and keeps syncing everything else (G3). **G4:** ordinary reorganisation — renaming a folder, renaming a folder tree — never trips the guard and never wedges the daemon in a standing block (R10).
5. **Names N1–N3:** case-collision collapse, normalization collapse (both probe-gated), duplicates — nothing silently clobbered, everything reported. Cross-tree fold collisions resolve as §4.2 conflicts — never a quarantine, never a fold-duplicate minted on Drive (R19).
6. **Daemon:** heartbeat/liveness classification, activity with direction labels, control round-trips, reload hot/cold split **race-clean under event load** (R14), degradation to polling and recovery **without the daemon ever exiting** (R15), guard block visible and clearable while the daemon keeps running.
7. **Randomized convergence (FZ1):** a seeded fuzzer drives random op sequences (edits/moves/deletes/conflicts across N simulated machines, random crash points) and asserts convergence and no-content-loss; failures replay deterministically from the seed. *(Adopted 2026-07-17 from Dropbox's testing approach.)* The oracle also asserts **identity stability, scoped to renames the pairing evidence supports** — a rename of a file whose content is unchanged since the last sync, or of a directory with at least one surviving paired child, preserves the id and plans zero delete-class actions; an edited-and-renamed file, or an empty directory, legitimately falls back to delete + create per §4.3, and the fuzzer generates the ops so it knows which renames qualify — and the **§4.5 structural invariant** (no two actions in the concurrent transfer stage on one rel_path or an ancestor/descendant pair). *(Strengthened 2026-07-18: convergence-plus-no-content-loss alone passes a folder rename that silently churns the Drive id, which is how W1.8's A1 escaped every existing test. Scoped later the same day: unscoped, both assertions flagged decided-correct behavior — the empty-dir rename and §4.3's unpaired remainders. See decisions.md.)* **Implemented 2026-07-23, W4** (`internal/engine/fuzz_test.go`): convergence is realized as a zero-action/zero-failure fixed point with byte-identical machine and Drive trees; "no content loss" is that no-silent-divergence plus the §4.5 net, not a raw content-set check (unsound — edits/deletes drop content by design; the sound content-supersession model is deferred). Determinism via a pinned clock + monotonic mtimes. First run found **R25** (a coincident/crashed-move wedge, §4.6). Type clashes (C5) and the deferred directory-arm gaps are excluded from the convergence oracle by design and covered by their own tests. *(See decisions.md.)*
8. **Watcher scale (W1):** a tree of ≥50k files syncs under the daemon without descriptor exhaustion; killing the native watcher degrades to polling and recovers. *(Verified 2026-07-23, W3.3 — `TestScale`/`TestFSEventsScaleNoFDExhaustion`, gated by `SYNCKEEPER_SCALE_FILES`. At 50k on macOS: the FSEvents backend reports 0 unwatchable directories — a directory-tree stream holds no per-file descriptors; the fsnotify kqueue backend reports 403/500 unwatchable at the 10240 fd soft limit and latches to polling — graceful, not fatal (the degrade→sync→recover loop is R15); a full engine sync of 50 500 actions converges and the next cycle is idle.)*
9. **Soak:** 2-hour random-edit soak on both sides converges with no divergence — part of the release ritual, re-run per platform port. *(W3.5, 2026-07-23: `TestSoak` runs against the production watcher backend — FSEvents on darwin+cgo. **Full 2 h FSEvents gate PASSED 2026-07-24** (code `aefe4fe`): `--- PASS: TestSoak (7216.68s)`, converged on 16,344 files, clean `doctor` on both machines, no divergence — the last W3 step, closing W3. The original 2 h pass was 2026-07-09 on fsnotify.)*
10. **Multi-machine:** the adopt matrix passes simulated; a real ≥2-machine rollout runs a day under `watch` ending in a clean `doctor`.

## Roadmap

Ordering only — nothing here changes the behavior defined above.

1. **macOS** (daily driver): correctness fixes → **fuzzer** → watcher module with FSEvents + soak → daemon-first polish → real multi-machine rollout. *(Reordered 2026-07-18: the fuzzer moves ahead of the watcher. Correctness never depends on the watcher (§8.1), so FSEvents is the lowest risk-reduction work available, while three consecutive adversarial passes have found engine bugs the suite missed. See decisions.md.)*
2. **Linux**: fswatch/inotify + names + suite run on real hardware.
3. **Windows**: names hardening (reserved names, separators, length), fswatch/RDCW, rename-replace semantics verification, suite run.
4. **UI, after the CLI is solid** (Dropbox-style): tray/menu-bar app on the control socket; file-manager badges where an API exists.

## Appendix: Dropbox as reference model

Adopted (validated against public Dropbox engineering material): the three-tree model with the synced tree as merge base (§4.1 — identical to Nucleus); node identity by id, not path (§4.3); single-owner control loop with offloaded I/O (§4.5, §8.1); daemon + thin clients over local IPC (§8); conflicted-copy semantics (§4.2); staged atomic writes (§7); OS-native watching, per platform (§10); normalization handling (§5, adapted to client-side folding); seeded randomized sync testing with deterministic replay (§16.7).

Not adopted, with reasons: block-level delta sync, LAN sync, streaming sync (require a block-speaking server; Drive replaces whole files — §9); online-only placeholders (deep OS integration; someday); push notifications (need a public endpoint; polling instead).
