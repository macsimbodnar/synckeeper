id:         S022
goal:       One action that fails every cycle keeps three safeguards switched off for as
            long as it lasts, and a file written without pause (a log, a live database
            inside the sync folder) is now a designed source of exactly that: S011
            reports each partial upload as a failed action. With `res.Failed > 0`:
            `purgeQuarantine` does not run (`internal/engine/engine.go:282`), so the
            quarantine outgrows `quarantine_retention_days` without bound;
            `reportDeletions` returns early (`engine.go:296`), so a mass deletion
            executed in those cycles raises no `LargeDeletion` warning; `recordActivity`
            (`internal/watch/status.go:174`) writes only the cycle summary and the
            error rows — no per-action entries for any other file — and summary and
            error alternate, so they never fold into a repeat count and flush the
            500-row ring (MANUAL §6 claimed a persistent fault cannot evict history).
            Found by S011's implementer and fast check (2026-11-11), by reading. The
            every-cycle failure for such a file predates S011 (the old code returned
            an error too, and made conflict copies besides).
accepts:    Red-first tests: with one action failing every cycle, (a) the quarantine is
            still purged on schedule, (b) a mass deletion executed in such a cycle is
            still reported, (c) the other files' activity entries are still recorded,
            (d) the repeating failure folds into one counted entry. Whether a partial
            upload stays a *failed* action or becomes its own "still being written"
            outcome (changes `activity` wording and `sync`'s exit status) is confirmed
            with Max before it is built. testing.md rows; MANUAL §6/§8; specs §8.
touches:    internal/engine, internal/watch, internal/statedb (activity folding),
            internal/executor (only if the partial upload gets its own outcome),
            MANUAL.md, adocs/specs.md, adocs/testing.md
excludes:   S003 (skips in `activity`), S004, S005 — neighbours, separate.
closes:
paused_by:
author:
done:
