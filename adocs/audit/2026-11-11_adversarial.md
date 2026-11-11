# Adversarial audit — 2026-11-11 — commit 1cea1b769eeb754611f442043ea3c61f3486ce61

Scope: whole project, big issues only (owner's boundary). Question covered: correctness and fitness for public distribution.

**Verdict: not fit for public distribution at this commit.** F01 and F02 each empty the Drive folder — and, through it, every other machine — from an ordinary configuration or a documented command, deterministically, with no race and no crash. F03–F05 break stated invariants. F06 is a legal blocker. Everything destructive below lands in a bin (Drive's ~30 days, the system bin), so it is recoverable *if noticed*; none of it is reported to the user as a deletion they did not ask for.

How it was measured: `go build ./... && go vet ./... && go test ./...` green; `go test -race ./...` green; `GOOS=linux go build/vet ./...` clean; every finding marked *reproduced* has a test that fails at `1cea1b7`. Audited on darwin/arm64, Go 1.27.4, against the in-memory Drive fake. **Not done:** no live Drive, no Linux hardware, Windows not examined.

Regression tests (new, this run): `internal/engine/audit_2026_11_11_test.go`, `cmd/synckeeper/audit_2026_11_11_test.go`. They are **gated** behind `SYNCKEEPER_AUDIT_REPRO=1` and skip by default, so `make test` stays green (8 skipped); ungated they are 8 of 8 red. The gate is the auditor's choice, made so that committing this report does not break the green-at-every-commit rule — remove it per test in the fixing step.

```
SYNCKEEPER_AUDIT_REPRO=1 go test ./internal/engine ./cmd/synckeeper -run TestAudit20261111 -count=1 -v
```

## Findings

### 2026-11-11_adversarial-F01 — Critical — A symlinked `sync_dir` is scanned as empty: the Drive folder is trashed and every other machine emptied

Status: planned

**Evidence** (reproduced):

- `internal/scanner/scanner.go:24` walks with `filepath.WalkDir(root, …)`. WalkDir does not follow a symlink given as its root: it visits the link and returns, no error. The snapshot is empty.
- `internal/guards/guards.go:39,42` checks the same path with `os.Stat` / `os.ReadDir`, which do follow it — the sanity guard passes.
- `internal/config/config.go:191` resolves `sync_dir` with `filepath.Abs` only; nothing in the non-test tree calls `EvalSymlinks` on it for the scan (only the FSEvents backend does, for its own stream, `internal/watch/fsevents_darwin.go:129`).
- Empty snapshot + full baseline = every row "deleted locally" → `TrashRemote`. Remote deletions are never guarded (`internal/guards/guards.go`, `CheckMassDelete` counts `QuarantineLocal` only).
- `TestAudit20261111_F01_SymlinkedSyncDirEmptiesDrive`, cycle 1 = `init`'s merge, cycle 2 = the daemon's first cycle:

```
a symlinked sync dir was scanned as empty and read as a deletion: 2 trash_remote actions, 0 of 2 files left in the Drive folder, 0 of 2 files left on machine b; nobody deleted anything
```

**Impact:** `~/Synckeeper -> /mnt/data/Synckeeper` (the obvious workaround for MANUAL §9's "mounting only the sync folder from a separate volume is unsupported") on any one machine. `init` downloads the tree; the next cycle trashes the whole Drive folder; every other machine moves its tree to its system bin. No error, no failed action, no warning, no guard — both cycles report success. By the same empty snapshot (reading, not run), files already in the linked folder are never uploaded. Contradicts MANUAL's headline "Never silently loses or corrupts a file".

**Suggested resolution:** resolve the root before walking (in `Config.SyncDir` or `scanner.Scan`). Add the structural net as well, since this is the third route to "scan saw nothing" (unmounted volume, unreadable dir, now this): a scan that returns zero entries while `os.ReadDir(root)` is non-empty is a hard error. Scanner + engine tests with a symlinked root.

### 2026-11-11_adversarial-F02 — Critical — `init` over an existing state DB with the sync folder gone empties the Drive folder

Status: planned

**Evidence** (reproduced):

- `cmd/synckeeper/init.go:105` runs `os.MkdirAll(syncDir, 0o755)` **before** the merge at `init.go:128`.
- So `guards.EnsureSyncDir` (`internal/guards/guards.go:38-58`) never sees the folder missing, never returns `recreated`, and `engine.go:156-161` never resets the baseline. The engine sees an existing empty directory beside a full baseline — the "emptied folder is a real deletion" arm.
- `TestAudit20261111_F02_InitWithMissingSyncDirEmptiesDrive` replays `init`'s three steps in `init`'s order (`initialize` → `MkdirAll` → `Sync{PreferNewer}`; the command itself has no sign-in seam):

```
`init` deleted from Drive: 2 trash_remote actions planned, 0 of 3 files left in the Drive folder, 0 of 3 back on disk (want 3 and 3: recreated and downloaded back)
```

- Existing coverage does not reach it: `TestF5SyncDirVanishedIsRecreatedNotPropagated` drives the engine directly, without `init`'s `MkdirAll`, and stays green.

**Impact:** who hits it — anyone whose sync folder is gone while `state.db` survives, and who runs the documented recovery command: folder deleted by mistake with the service stopped; a replaced data disk; a new machine with the config dir restored from backup (dotfiles, `~/.config`) but not the data, then MANUAL §2's `synckeeper init`. The restored `token.json` signs in without a browser, so nothing interrupts it. Result: whole Drive folder in Drive's bin, then every other machine's tree in its system bin. Directly contradicts MANUAL §1 "**`init` never deletes, and is safe to re-run**", §6 "Deleting the sync folder itself is never a deletion", spec §11 "structurally cannot produce a delete" (true only over an empty baseline, which `init` does not guarantee).

Same root cause, not separately run: the baseline is not bound to the sync folder's identity (no path or marker in `statedb` meta), so pointing `local.sync_dir` at a pre-created empty folder and restarting takes the same arm.

**Suggested resolution:** `init` must not create the folder ahead of the engine — drop `init.go:105` and let `EnsureSyncDir` recreate-and-reset, or have `init` reset the baseline whenever it had to create it. Consider binding the baseline to the folder (path or a marker recorded in meta; mismatch → reset, as `root.Resolve` does for the Drive side). Move the F5 acceptance test onto the `init` path.

### 2026-11-11_adversarial-F03 — High — An interrupted mirror rebuild (`init` re-run, `doctor --repair`) is read as a mass deletion on Drive

Status: planned

**Evidence** (reproduced):

- `internal/remotedelta/remotedelta.go:52-67` `ForceFullWalk`: `ClearRemoteNodes()` (`:57`), then one auto-committed upsert per node across many `List` calls, page token and marker written last. No transaction, and `MetaWalkDone` is **not** retracted first — it stays `"1"` from the earlier install.
- Callers on a live baseline: `cmd/synckeeper/init.go:259` (the `keep` path of a re-run) and `internal/doctor/doctor.go:210`. `root.go:133,154` are safe: `commitIdentity` deletes the marker in the same transaction.
- A walk that stops part-way leaves a partial mirror that `Refresh` (`remotedelta.go:32`) trusts. Baseline rows absent from it plan `QuarantineLocal`.
- Network errors are not retried: `internal/driveclient/retry.go:20-24` retries only `*googleapi.Error`; a reset connection fails the walk at once. A killed `init` (Ctrl-C; the walk prints no progress) leaves the same state.
- `TestAudit20261111_F03_InterruptedMirrorRebuildBinsLocalFiles` (connection drops on the second `List` of a re-run `initialize`, then one ordinary cycle):

```
an interrupted mirror rebuild was read as a deletion on Drive: 9 local delete actions planned, 1 of 10 local files left on disk, 10 of 10 still on Drive
```

**Impact:** the two commands MANUAL offers for recovery can each arm a mass removal of local files on the next cycle. Files go to the system bin (on a no-bin machine the quarantine, where the mass-delete guard applies past its threshold). Drive is untouched, but the mirror no longer knows those ids, so the machine silently stops holding those files until they next change on Drive or a later full walk. Breaks spec §12 "Re-running `init` over an existing DB must leave the remote mirror coherent" and INV-6.

**Suggested resolution:** make the rebuild crash-safe: retract `MetaWalkDone` before clearing (an interrupted run then re-walks on the next `Refresh`), or build into a transaction / staging table and swap. Fault test at every `List`.

### 2026-11-11_adversarial-F04 — High — A local edit's upload overwrites a concurrent remote edit: last writer wins, no conflict copy (INV-5)

Status: planned

**Evidence** (reproduced):

- `internal/executor/executor.go:625` calls `Client.Update(ctx, a.FileID, …)` with no check that Drive still holds the version the plan was built on; the `UpdateRemote` action carries no expected remote version at all (`internal/reconcile/plan.go:364`). `internal/driveclient/real.go` `Update` sends no precondition.
- Spec §7's gate re-stats every **local** path before mutating it. Nothing is the remote counterpart: `Update` (`:625`), `Trash` (`:763`), `Move` (`:536`) all act on the cycle-start mirror.
- `TestAudit20261111_F04_UpdateRemoteOverwritesConcurrentRemoteEdit` — B's upload lands after A's refresh, before A's `Update`; then both machines settle:

```
audit_2026_11_11_test.go:207: [a] one of two concurrent edits is gone with no conflict copy: files on disk = map[notes.txt:edit made on A]
audit_2026_11_11_test.go:207: [b] one of two concurrent edits is gone with no conflict copy: files on disk = map[notes.txt:edit made on A]
```

**Impact:** two machines edit one file; the window is machine A's refresh → its `Update` call, i.e. its scan plus its transfer queue — short on an idle tree, as long as the backlog otherwise (not measured). B's edit is replaced on Drive and then **on B's own disk** (B sees "remote changed, local unchanged" and downloads over it). No conflict copy, no activity entry of kind `conflict`. Only Drive's revision history still has B's bytes. Same gap, by reading and not run: a `TrashRemote` racing a remote edit bins the edit on both sides. Contradicts INV-5 and MANUAL "Conflicting edits make a conflict copy, never last-writer-wins". Not confirmed: the size of the window on real Drive (fake only).

**Suggested resolution:** a remote-write gate mirroring §7: carry the expected Drive version/md5 on `UpdateRemote`, `TrashRemote`, `MoveRemote`; `Get(fileID)` immediately before the mutation; refuse on drift and replan (the next cycle then conflicts correctly). Narrows the window to one round trip.

### 2026-11-11_adversarial-F05 — High — A file still being written while it uploads ends up truncated under its real name on every machine

Status: planned — known (MANUAL §8 row 2, decisions.md 2026-07-31 "F3", accepted then as "nothing lost"); re-raised because the trigger and the outcome are both wider than recorded, and no step plans it

**Evidence** (reproduced):

- `internal/executor/executor.go:584-597`: hash the file, then open and stream it. `:639-641`: on md5 mismatch return an error — with the id of the item Drive just stored (`remote.ID`) in hand, and left there.
- Next cycle the leftover is seeded (`seedOrphanCreates`) and §4.2 resolves "both new, different md5" in Drive's favour.
- `TestAudit20261111_F05_FileWrittenDuringUploadIsTruncatedAtCanonicalName` — a file grows during the upload; nobody edits anything:

```
[a] one file was copied into the sync folder and nobody edited it anywhere; want exactly video.mkv = "PART-1|PART-2|PART-3", have 2 file(s): map[video (conflict a 2026-11-11_111016).mkv:PART-1|PART-2|PART-3 video.mkv:PART-1|PART-2|]
[b] (identical)
```

**Impact:** MANUAL describes "You edit a file while it's uploading". The actual trigger is any write still in progress when a cycle reaches the file: a large copy into the folder, a browser download, an export. The poll tick fires every 45 s regardless of the debounce, so any write longer than that is exposed. Outcome: the **canonical name holds a truncated file** on every machine and the complete one sits under a conflict name. For an existing file (`UpdateRemote`, same commit path; by reading, not run) the content Drive keeps can be a mix of two versions that never existed. "Nothing lost" holds; "never … corrupts a file" does not. Not run: a real browser download, where the browser's final rename would land on the truncated file.

**Suggested resolution:** on mismatch after a create, trash `remote.ID` before returning — exact, no listing, and not the blind re-upload W17 ruled out. For `Update`, re-upload within the action, or record that the new Drive version is this machine's own failed write so the next cycle does not treat it as a foreign edit. Independently: skip files whose stat changed between hash and end of stream / modified in the last few seconds. Reword MANUAL §8 to the real trigger now.

### 2026-11-11_adversarial-F06 — High (distribution blocker) — No license

Status: planned

**Evidence:** `git ls-files | grep -i -E 'licen|copying|notice'` → no output. `grep -n -i licen README.md MANUAL.md go.mod` → no output.

**Impact:** published without a license the code is all-rights-reserved: nobody may legally use, modify, or redistribute it, and MANUAL §1 tells users to build from source.

**Suggested resolution:** owner picks a license; add `LICENSE` and one README line. (Dependencies were not license-audited in this run.)

### 2026-11-11_adversarial-F07 — Medium — A tracked folder replaced by a symlink is trashed on Drive

Status: planned

**Evidence** (reproduced): `internal/scanner/scanner.go:68-71` reports a symlink as a skip and leaves the path out of the snapshot. Only `Skip.Unreadable` is expanded into a held-harmless set (`internal/engine/engine.go:358-378`); a symlink skip is not. `TestAudit20261111_F07_FolderReplacedBySymlinkIsTrashedOnDrive`:

```
a folder replaced by a symlink to itself was read as deleted: 1 trash_remote actions, 1 of 3 files left on Drive, 1 of 3 left on machine b (skips reported: [{photos symlink; not followed  false false}])
```

**Impact:** moving a large subfolder to another disk and symlinking it back sends its Drive folder to Drive's bin and removes it from every other machine. Under the daemon the skip is shown nowhere — it is written to meta `last_skipped` (`engine.go:273`), which no non-test code reads; S003 is still open — so the only trace is an ordinary `trash` activity row.

**Suggested resolution:** hold baseline rows under any present-but-unscannable path harmless — the W18-G mechanism, generalized from "unreadable" to symlink / non-regular.

### 2026-11-11_adversarial-F08 — Medium — One unreadable file stops all syncing, every cycle

Status: planned

**Evidence** (reproduced): `internal/scanner/scanner.go:99-101` returns the hash error and fails the scan. `TestAudit20261111_F08_OneUnreadableFileWedgesEveryCycle`:

```
cycle 1: one unreadable file failed the whole cycle, so ok.txt never syncs: scan local dir: hash locked.txt: open …/sync/locked.txt: permission denied
```

**Impact:** the file arm of the wedge W18-G fixed for directories (decisions.md 2026-07-31 "F4"). One `chmod 000` or root-owned file anywhere in the tree and nothing syncs in either direction until it is fixed; fail-safe, visible only as `last error` / backoff.

**Suggested resolution:** same treatment as an unreadable directory: reported skip, row held harmless, rest of the tree syncs.

### 2026-11-11_adversarial-F09 — Medium — The R25 wedge reproduces at the default crash probability; the documented deep-fuzz command is red

Status: planned

**Evidence:**

```
$ SYNCKEEPER_FUZZ_RUNS=60 SYNCKEEPER_FUZZ_STEPS=150 go test ./internal/engine -run TestFuzzConvergence     # CLAUDE.md, verbatim
--- FAIL: TestFuzzConvergence/seed_45
    fuzz_test.go:663: FUZZ FAILURE (seed 45): op-phase sync [c] hard-errored: plan violates §4.5: upload and download both act on "dir15/f1.txt" in the concurrent transfer stage; refusing the plan, replanning next cycle
$ SYNCKEEPER_FUZZ_SEED=45 SYNCKEEPER_FUZZ_RUNS=1 SYNCKEEPER_FUZZ_STEPS=150 go test ./internal/engine -run TestFuzzConvergence   # deterministic replay: FAIL
$ SYNCKEEPER_FUZZ_CRASH=0 … (same seed)                                                                      # ok
```

Same failure on `git archive` copies of `90ee8ca` (2026-08-01) and `d5533ce` (2026-08-23) — pre-existing, not from S001.

**Impact:** an availability defect, fail-safe (the refusal is the §4.5 check working), already planned as S002. What is new is that its recorded premise is false: decisions.md:60 says it "reproduces only at `SYNCKEEPER_FUZZ_CRASH=1`" and plan_history.md:186 that "every recorded sweep is green" at the default 0.12 — that is what put S002 behind the Linux port — and MANUAL §8 says it "needs crashes mid-cycle repeatedly; unlikely in normal use". The command CLAUDE.md gives for the deep sweep fails at the default. Not measured: how many crashes seed 45 needs.

**Suggested resolution:** re-triage S002's position on this evidence; add seed 45 to its `accepts`; correct the three records.

### 2026-11-11_adversarial-F10 — Medium — The Drive folder being in Drive's bin goes unnoticed by `watch` and `sync` (partly unconfirmed)

Status: planned

**Evidence:** spec §12: a root "genuinely gone (404, or in Drive's bin) is **recreated**" and "every resolution goes through `internal/root.Resolve`". Only `init.go:245` and `doctor.go:193` call it. `cmd/synckeeper/watch.go:39` and `sync.go:44` read `root_folder_id` from meta and never look at the folder. `remotedelta.consumeChanges` handles a trashed root as a no-op node delete. Against the fake, `TestAudit20261111_F10_BinnedDriveRootIsSyncedIntoSilently`:

```
the Drive folder is in Drive's bin and the cycle reported clean success: 1 actions executed into the binned folder, 0 failed, 0 skips, no error
```

**Not confirmed** (no live Drive in this run): what the real change feed emits for the folder's descendants when it is binned, and when the bin purges it. If descendants are reported, every machine moves its tree to its system bin at once; if not, machines keep syncing through a folder on a 30-day purge clock with nothing saying so. Either way the code does not do what §12 states.

**Impact:** a user who bins the "Synckeeper" folder in the Drive web UI gets one of those two outcomes instead of the recreate-and-re-upload the spec promises.

**Suggested resolution:** settle the feed behaviour with the live suite first. Then have the cycle check the root (a `Get` per cycle, or on the root's own change entry) and route "gone/binned" through `root.Resolve` or a hard, visible error.

### 2026-11-11_adversarial-F11 — Medium — Linux: the mass-delete guard asks the home trash, deletions use the per-volume one (read, not run)

Status: planned

**Evidence** (code reading only — Linux-tagged, auditor on darwin): `internal/trash/trash_linux.go:224-234` `available()` tests the **home** trash alone. `trashDirFor` (`:137-162`) sends a sync dir on another filesystem to `<mount>/.Trash-$uid`, created with `MkdirAll` at `:52`. `internal/engine/engine.go:224-225` feeds `Available()` to `CheckMassDelete`. Where the mount root is not user-writable (`/mnt/data` root-owned, sync dir a user-owned folder inside it), every `MoveToTrash` fails → `errTrashUnusable` → quarantine (`internal/executor/executor.go:874-887`), across devices by copy (`internal/executor/localwrite.go:133-165`).

**Impact:** on that layout a mass deletion arriving from Drive runs unguarded into the private quarantine — the one case INV-4 says is held for confirmation — is copied byte for byte into the config dir's volume, and is purged after `quarantine_retention_days`. It is reported after the fact (`QuarantineFell`).

**Suggested resolution:** probe the trash the sync dir will actually use; run it on Linux hardware with L8.

## Verdicts on prior findings

No earlier report exists under `adocs/audit/` (only `.gitkeep`), so there are no finding ids to re-measure. Earlier reviews are recorded in `decisions.md` (2026-07-31 "F1–F5", 2026-08-23), outside this directory and without audit ids. Where this run touched them: their F3 is re-measured as F05 above (still reproduces, wider than recorded); their F4 was fixed for directories and its file arm is F08; their F1/F2/F5 were not re-measured.

## Seen, not raised

Recorded owner decisions, left alone: an emptied or unmounted-but-present sync dir propagates as a deletion with no guard (W18-D, W14; F01 and F02 are new routes into that same arm, which is why the arm's lack of any net matters); `sync_dir` containing the config dir uploads `token.json` (MANUAL §9); unbounded daemon log (L2b); Linux L8 gates outstanding while MANUAL §9 says "validated on hardware" (status.md already owes this to L7).

Checked, clean: no OAuth client secret, client id or token in any of the 164 commits (`git log --all -p` pattern scan); PKCE S256 + state on the loopback flow; `token.json` 0600 enforced on load; control socket inside a 0700 dir. Below the owner's threshold and not written up: a 10-character fragment of the scrubbed client secret quoted in `adocs/worklog.md:284`; `testing.md` row F5 still says "hard error".
