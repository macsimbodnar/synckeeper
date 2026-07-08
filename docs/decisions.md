# Synckeeper — Decision Log

Dated record of scope changes, spec deviations, and non-obvious technical choices. Newest first. Add an entry *before or alongside* the change it describes.

Format:

```
## YYYY-MM-DD — Short title
**Context:** why the question came up.
**Decision:** what was chosen.
**Consequences:** what it affects; link the spec/plan section if it amends one.
```

---

## 2026-07-08 — Phase 2 implementation decisions

**Context:** hardening surfaced how to simulate crashes and what repair may touch.

- **Fault injection aborts ops via error, not panic/exit.** A panic inside a transfer-pool goroutine would kill the test binary. Error-abort leaves the identical on-disk/remote/DB state as a crash at that checkpoint, except deferred temp cleanup — covered separately by planting orphan temps. A genuine `kill -9` mid-upload was run manually against real Drive and repaired cleanly.
- **`doctor --repair` only ever adds.** It restores meta (folder id by configured name, machine id, fresh page token), force-rebuilds the remote cache, and adopts md5-equal local/remote pairs into the baseline. It never trashes, quarantines, or overwrites: after a lost DB, one-sided files become plain uploads/downloads on the next sync, and deletions are structurally impossible (baseline ⊆ matched pairs).
- **Quarantine purge runs only after a fully successful sync** and only removes synckeeper's own dated folders (`YYYY-MM-DD`); anything else in the quarantine dir is left alone.
- **`cmd` grew a shared `appEnv` helper** (lock + config + DB + lazy Drive client) used by `sync` and `doctor`; `init` keeps its special flow.

## 2026-07-08 — Phase 1 implementation decisions

**Context:** building the sync engine surfaced choices the spec left open.

- **`internal/engine` package added** (not in the spec's repo layout): orchestrates one sync cycle (guards → remote delta → scan → reconcile → execute) so `sync`, phase-3 `watch`, and the scenario tests share one pipeline. `executor` stays limited to applying plans, per its spec role.
- **`remote_nodes` cache table (schema v2) stores all Drive changes, not just in-tree ones.** `changes.list` has no folder filter; tree membership is decided at snapshot time by walking parent links from the root folder. Costs a few rows per Drive file; personal-scale fine. Out-of-tree moves fall out naturally (parent chain breaks → remote delete).
- **Crash resume is discard-and-replan, not op resumption.** Stale pending_ops are dropped and orphan temps deleted at sync start; the fresh plan self-heals partial effects (an uploaded-but-uncommitted file reappears as both-new-same-md5 → adopt). pending_ops still journals every action, so phase 2's fault tests can verify the model and add targeted repair if F1–F3 reveal gaps.
- **Subtree deletes trash each file/folder individually, bottom-up** rather than trashing the top folder in one call: uniform op model and per-item journaling, at the cost of more Drive-trash entries. Restore-from-trash is a manual rescue path anyway.
- **Guards G1/G2 landed in phase 1** (spec put them in phase 2): once deletes propagate, running without the mass-delete and empty-dir guards was too risky even for daily personal use.
- **Deleting the last tracked file requires a non-empty dir**: G2 counts all directory entries, so an empty-dir-after-legitimate-delete is indistinguishable from an unmount and errors. Workaround: any other file (even an ignored one) in the dir. Revisit in phase 2 if it annoys.
- **Real Drive bumps a file's version shortly after upload** (post-processing), so the sync after an upload does one `record` row refresh with no transfer. Benign and self-limiting; noted so nobody chases it as a bug.

## 2026-07-06 — Baseline decisions inherited from the spec

Recorded here so future deviations have something to deviate from:

- Pure-Go only, `CGO_ENABLED=0`: cross-compilation from one machine is a hard requirement; any dependency pulling cgo is rejected.
- `modernc.org/sqlite` over mattn/go-sqlite3: the latter is cgo.
- Polling over push notifications: webhooks need a public HTTPS endpoint; unacceptable for a personal tool.
- Quarantine dir over OS-native trash: cross-platform Go trash libraries are immature; quarantine is simpler and equally safe.
- OAuth consent screen published to Production but unverified: Testing status expires refresh tokens after 7 days, which breaks a daemon.
- `reconcile` is a pure function: the correctness core must be exhaustively unit-testable with no I/O.
- Edit beats delete, always: a resurrected file is a nuisance; a lost edit is data loss.
- Remote wins the canonical name in conflicts: deterministic across machines (all machines agree on what Drive holds), and the local version is preserved as the conflicted copy — nothing is lost either way.
