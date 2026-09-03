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
closes:
paused_by:
author:
done:
