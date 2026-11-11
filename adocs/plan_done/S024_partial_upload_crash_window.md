id:         S024
goal:       The crash window S011 left open. A file still being written is uploaded,
            Drive stores the partial bytes, and the process is killed — or Drive's reply
            is lost — before `commitPartialUpload`'s transaction (`CPUploadBeforeCommit`).
            Nothing local records that those bytes are this machine's own, so the next
            cycle reads them as a stranger's: the partial upload keeps the real name on
            every machine and the complete file becomes the conflict copy (the pre-S011
            outcome; nothing lost). Kill reproduced by the skipped
            `TestS011CrashAfterPartialUploadLandsResidual` (create and update arms);
            lost reply from code reading. Part of audit 2026-11-11_adversarial F05.
accepts:    That test un-skipped and green on both arms: one complete file, real name,
            every machine, no conflict copy — while another machine's genuine concurrent
            edit is still never overwritten (red-first test for that too). testing.md
            rows; MANUAL §8 row leaves the list; specs §7.
touches:    internal/executor, internal/driveclient (real + fake), internal/statedb
            (only with Max's go-ahead for a schema change), MANUAL.md, adocs/specs.md,
            adocs/testing.md
excludes:   Nothing is built before Max picks the mechanism — both change something
            durable: tagging each upload with its author on Drive (`appProperties`;
            Client interface and fake change, every uploaded file carries the tag), or
            stamping the id/expected md5 in the pending-op journal before the upload
            (W17's not-taken option; covers the create arm only, and the journal is
            discard-and-replan today).
closes:
paused_by:
author:
done:       2026-11-11. NOT BUILT — closed by decision, not by work: Max chose to accept
            the residual (DEC-007); the crash window stays open and documented (MANUAL §8; skipped test `TestS011CrashAfterPartialUploadLandsResidual`).
