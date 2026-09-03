# Phase 7 — macOS experience

**Goal:** make Synckeeper feel native on macOS (the primary platform): visible sync status and control, before any cross-platform work.
**Exit criterion (amended 2026-07-16/17):** activity shows change direction; case collisions are handled safely. (Finder sidebar abandoned — no viable API, see decisions.md 2026-07-16; tray and badges moved to plan.md workstream W9.)
**Status:** in progress — quick wins first (chosen 2026-07-14). Sequenced ahead of phase 5's Windows/Linux specifics per the 2026-07-14 replan (macOS-first; other architectures last).

## Quick wins (first)
- [x] **Activity direction** (done 2026-07-14): every `activity`/`status` event is labeled `local→drive` / `drive→local` / `conflict`. The direction (`source`) is derived from the action type at record time and stored via statedb migration v4, so `status --json` and a future tray get it too. Verified end-to-end (migration on a v3 DB → v4, and the rendered output).
- [~] **Finder sidebar:** BLOCKED — not reliably achievable on macOS 14 by any public API (investigated 2026-07-16). The favorites list moved behind `sharedfilelistd` with no public write API; the legacy `LSSharedFileList` is deprecated and *disconnected from the real sidebar* on Sonoma (verified in Swift: `LSSharedFileListCopySnapshot` on `kLSSharedFileListFavoriteItems` returns **0 items** — it can't even see the user's real Documents/Desktop favorites, and inserts don't show in Finder). Driving it via `osascript`/JXA is additionally unstable (type errors, segfaults). The `.sfl3` is TCC-protected and stores security-scoped bookmark blobs that only Apple's frameworks can forge (cgo). Recommendation: drop, or revisit only if a supported API appears. See decisions.md 2026-07-16.
- [x] **Case-collision safety (done 2026-07-14):** on a case-insensitive sync dir (probed via `names.CaseInsensitiveFS`), `remotedelta.Snapshot` collapses Drive siblings differing only by case — keeps the first by id, skips + reports the rest, so a download can never silently clobber another. Case-only local renames were already handled by the existing local move-pairing (same md5+size ⇒ `move_remote`), now covered by a test. Deterministic Snapshot unit test + FS-probe test + an APFS-conditional rename test. This is the macOS slice of phase 5.

## Tray / menu-bar icon (was phase 6 stage 3, macOS)
- [ ] Separate cgo-allowed binary (`cmd/synckeeper-tray`), excluded from `make build-all`; NSStatusBar icon + menu (Sync now / Pause / Resume / Open folder / Open logs / Quit), talking to the control socket; mode-reflecting icon; `subscribe` streaming for live status. Details in [phase-6.md](phase-6.md) Stage 3.

## Finder sync badges
- [ ] FinderSync app-extension overlays (synced / syncing / conflict) inside a macOS app bundle. Most involved: app bundle + code signing + extension lifecycle. Reads state from the daemon (socket or DB).

## Deferred to last — other architectures ([phase-5.md](phase-5.md))
- Windows name hardening (reserved names, illegal chars, long paths, NTFS rename atomicity), cross-platform tray variants (Linux DBus StatusNotifierItem, Windows Shell_NotifyIcon), and the full test suite on real Linux + Windows.

## Verification
- [ ] `activity` and `status` show direction; `status --json` carries it.
- [ ] Synckeeper folder appears in Finder sidebar after setup.
- [ ] Case-collision tests pass on APFS; nothing silently overwritten.
- [ ] Tray icon reflects state and drives the daemon on macOS.
