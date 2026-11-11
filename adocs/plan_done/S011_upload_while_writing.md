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
author:     Opus subagent, coordinated by Claude (agent); started 2026-11-11 at Max's direction (DEC-002)
done:       2026-11-11. `TestAudit20261111_F05_FileWrittenDuringUploadIsTruncatedAtCanonicalName`
            passes ungated, assertions unchanged. On an md5 mismatch after `Upload` or
            the gated `Update`, `commitPartialUpload` commits the baseline row as
            holding Drive's bytes (`ContentMD5 = DriveMD5 =` Drive's md5, local size
            `unknownLocalSize` = -1 so the scanner always rehashes) with the mirror row
            (W16), and still reports the action failed. Next cycle: local changed,
            remote unchanged → `UpdateRemote` on the same id; no second item (W17), no
            conflict copy, no added API call. Same mechanism for create and update.
            New tests: `TestS011UpdateOfFileStillBeingWrittenKeepsCompleteFileAtCanonicalName`,
            `TestS011FileWrittenAcrossCyclesIsNeverStarvedNorConflicted` (5 cycles),
            and the skipped `TestS011CrashAfterPartialUploadLandsResidual` pinning the
            open window (testing.md S011.1–S011.5). Deviation from the suggested shape,
            accepted: the created item is not trashed — trashing would starve a
            continuously written file and leave a truncated copy in Drive's bin per
            cycle. Verified by the coordinator: `go build && go vet`, `gofmt -l`,
            `go test -count=1 ./...`, `-race` on executor/engine green on darwin;
            `GOOS=linux go build/vet` clean. Implementer's fuzz comparison: 60×150
            fails on seeds 1, 28, 45 before and after (S002). Not run on real Drive.
            NOT closed, MANUAL §8 keeps the row: a kill or lost reply between Drive
            storing the partial and the commit gives the old outcome → S024.
            Fast check (REVIEW): every reader of the -1 row checked, no path downloads
            or quarantines over the complete file, no extra crash window, the gate does
            not refuse the follow-up. Docs corrected here on its two findings: "never a
            conflict copy" has one reproduced exception (both machines rename the
            partial to the same name — a pre-existing planner rule, S023); and a file
            written without pause fails every cycle, which keeps three `Failed == 0`
            gates shut (S022; the per-cycle failure itself predates this step).
