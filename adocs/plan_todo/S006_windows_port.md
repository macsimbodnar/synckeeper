id:         S006
goal:       Third platform: Windows.
accepts:    Names hardening (reserved names, illegal characters, trailing dots and
            spaces, long paths); the fswatch/ReadDirectoryChangesW backend;
            rename-replace semantics under the journal (F3 on-platform); Task
            Scheduler service install; a native build; the full suite and the soak
            green on Windows hardware.
touches:    internal/names, internal/watch, internal/service, internal/trash,
            MANUAL.md, README.md
excludes:   Cross-compilation — builds are native per platform (specs §10).
            Noted 2026-11-11 (S014 fast check, by reading): the scanner skips only
            `fs.ErrPermission`; on Windows ERROR_SHARING_VIOLATION (32) and
            ERROR_LOCK_VIOLATION (33) are not mapped to it, so a file another process
            holds open exclusively (Outlook PST, an open database, a VM disk) fails the
            whole scan for as long as it is open. Also unmeasured anywhere: macOS
            dataless iCloud / File Provider placeholders (hashing fetches them; offline
            likely EIO/ETIMEDOUT, which fails the scan).
closes:
paused_by:
author:
done:
