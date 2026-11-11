id:         S011
goal:       A file still being written while it uploads ends up truncated under its real
            name on every machine; the complete file becomes the conflict copy.
            `internal/executor/executor.go:584-597` hashes then streams, and on md5
            mismatch (`:639-641`) returns an error leaving the item Drive just stored.
            Known as MANUAL §8 row 2 (decisions.md 2026-07-31 "F3", accepted as
            "nothing lost"); re-raised because the trigger is any in-progress write
            longer than a poll tick (large copy, browser download), not "you edit".
            Found by audit 2026-11-11_adversarial (F05, High).
accepts:    `TestAudit20261111_F05_FileWrittenDuringUploadIsTruncatedAtCanonicalName`
            passes ungated: exactly one `video.mkv`, complete, on both machines, no
            conflict copy. The `UpdateRemote` arm has its own red-first test. MANUAL
            §8 row leaves the list in the fix's commit; testing.md row lands with the
            fix.
touches:    internal/executor, internal/scanner (if in-flight files are skipped),
            MANUAL.md, adocs/specs.md §7, adocs/testing.md
excludes:   The blind re-upload W17 ruled out.
closes:     2026-11-11_adversarial-F05
paused_by:
author:
done:
