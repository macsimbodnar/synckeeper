id:         S017
goal:       The baseline is not bound to the sync folder's identity. When `sync_dir`
            starts naming a different folder — a symlink retargeted (`ln -sfn`, a
            dotfile manager), or `local.sync_dir` edited and the daemon restarted —
            the next cycle scans the new folder against the old baseline: an empty
            target reads as every file deleted (Drive folder trashed, other machines
            binned; the empty-scan net does not fire, both sides are empty), a
            different one as mass delete + mass create. Found by S007's fast check
            (2026-11-11); named as "same root cause, not separately run" in audit
            2026-11-11_adversarial F02. S007 made the link case reachable without
            touching synckeeper: the engine re-resolves the link every cycle.
            A third route, pre-existing, found by S008's fast check: the process is
            killed between `guards.EnsureSyncDir`'s `MkdirAll` and the engine's
            `ResetBaseline` (`internal/engine/engine.go:152-157`, separate steps) —
            the folder then exists empty beside a full baseline. The chosen shape
            must close it too (or the reset moves ahead of the mkdir).
accepts:    Red-first tests for both routes (retargeted link, edited `sync_dir`) show
            zero deletions planned on either side; the cycle either resets the
            baseline (as `EnsureSyncDir` does for a recreated folder) or stops with a
            visible error naming old and new folder — whichever Max picks. The
            FSEvents stream follows a same-volume retarget or the retarget is refused.
            testing.md rows; specs §3/§6/§14; MANUAL §6/§9.
touches:    internal/statedb (meta: recorded folder identity), internal/engine,
            internal/guards, internal/watch (fsevents_darwin refresh), MANUAL.md,
            adocs/specs.md, adocs/testing.md
excludes:   Nothing is built before Max picks the shape: what identifies the folder
            (resolved path, a marker file, device+inode) and what a mismatch does
            (reset the baseline and merge, or refuse). An emptied-but-same folder stays
            a real deletion (W18-D, W14).
closes:
paused_by:
author:
done:
