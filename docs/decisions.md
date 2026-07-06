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
