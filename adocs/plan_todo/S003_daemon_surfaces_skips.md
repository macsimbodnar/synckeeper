id:         S003
goal:       The daemon reports skipped files. Today skips (Google-native docs,
            symlinks, non-regular files, invalid names, Drive same-name duplicates)
            are reported only by a one-shot `sync`/`init`; under the daemon they are
            invisible. Finding D1 of the 2026-07-24 doc audit.
accepts:    A daemon cycle with a skipped Google-native file (or symlink) records a
            `skip` activity entry visible in both `status` recent-activity and
            `synckeeper activity`. Recurring identical skips do not flood the capped
            ring — the schema v7 fold (W19-2) collapses an entry identical to the
            newest row into its `count`, so W10 emits per-skip entries plainly and
            inherits the anti-flood property from the storage layer. testing.md row
            lands with the code.
touches:    internal/engine (Result.Skips already exists), internal/watch/status.go
            (`cycleDone` currently drops skips), internal/status (a direction-neutral
            `skip` kind and its label), adocs/testing.md, adocs/specs.md §5/§8.2
            dated note
excludes:   No sync-behavior change — this is a new observability surface only.
closes:
paused_by:
author:
done:
