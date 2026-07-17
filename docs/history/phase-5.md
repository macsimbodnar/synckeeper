# Phase 5 — Cross-platform hardening

**Goal:** the same binary behaves safely on Linux, macOS, and Windows filesystems with all their quirks.
**Exit criterion:** full test suite green on all three OSes.
**Status:** not started

## Tasks

### Case collisions (`internal/names`) — done 2026-07-14 (pulled into [phase 7](phase-7.md), it affects macOS/APFS)
- [x] Detect Drive trees containing names differing only by case (`a.txt` vs `A.txt`) targeting a case-insensitive local filesystem: keep one deterministically (first by id), skip + report the other; never silently overwrite. (`remotedelta.Snapshot` + `names.CaseInsensitiveFS` probe.)
- [x] Case-only local renames propagate as a remote rename, not delete+create (existing local move-pairing; covered by an APFS-conditional test).

### Windows-specific
- [ ] Long paths: `\\?\` prefix where paths exceed 260 chars.
- [ ] Reserved names (`CON`, `PRN`, `AUX`, `NUL`, `COM1`–`COM9`, `LPT1`–`LPT9`): skip and report.
- [ ] Illegal characters (`< > : " / \ | ? *`), trailing dots/spaces: skip and report.
- [ ] Verify rename-with-replace semantics of the atomic write path on a real NTFS volume; confirm the pending_ops journal covers any non-atomic window.

### Symlinks and specials
- [ ] Symlinks: never followed, reported in `status` (should already hold from phase 1 — add explicit tests).
- [ ] Non-regular files (sockets, fifos, devices): skipped and reported.

### Platform test runs
- [ ] Platform test cases from [testing.md](../testing.md) (case collision, reserved names, long paths, symlinks) implemented; case/reserved-name tests skip conditionally where the host FS can't express them.
- [ ] Full `go test ./...` run on real Linux, macOS, and Windows machines (documented manually — no CI in v1).
- [ ] Scenario S1–S8 + fault F1–F5 executed on Windows at least once (path handling differs most there).

## Verification

- [ ] Suite green on all three OSes; results recorded below with date and OS versions.

| OS | Version | Date | Result |
|---|---|---|---|
| Linux | | | |
| macOS | | | |
| Windows | | | |
