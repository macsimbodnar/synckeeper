id:         S014
goal:       One unreadable file fails the whole scan, so nothing syncs in either
            direction, every cycle (`internal/scanner/scanner.go:99-101`). The file arm
            of the directory wedge W18-G fixed (decisions.md 2026-07-31 "F4").
            Found by audit 2026-11-11_adversarial (F08, Medium).
accepts:    `TestAudit20261111_F08_OneUnreadableFileWedgesEveryCycle` passes ungated:
            the unreadable file is a reported skip, its baseline row is held harmless,
            the rest of the tree syncs. MANUAL §8 row leaves the list in the fix's
            commit; testing.md row lands with the fix.
touches:    internal/scanner, internal/engine, MANUAL.md, adocs/specs.md §5,
            adocs/testing.md
excludes:   A file whose `lstat` fails inside a readable directory — decided 2026-07-31
            to keep failing (the tree is moving under the scanner).
closes:     2026-11-11_adversarial-F08
paused_by:
author:     Opus subagent, coordinated by Claude (agent); started 2026-11-11 at Max's direction (DEC-002)
done:       2026-11-11. `TestAudit20261111_F08_OneUnreadableFileWedgesEveryCycle` passes
            ungated. A permission error while hashing (`errors.Is(err, fs.ErrPermission)`,
            on open or mid-read) is a skip marked `Unscanned` + `UnreadableFile`
            carrying the file's stat; S013's path hold keeps its row and plans nothing
            at its path on either side; the rest of the tree syncs. The daemon logs one
            WARN per file per occurrence (`warnUnreadable`, re-armed once readable).
            Every other hash error — `lstat` failure in a readable dir (`excludes`), a
            vanished file, an I/O error — still fails the cycle, by design.
            Two rounds, one fast check (REVIEW). The review reproduced that round 1
            turned the old fail-safe wedge into a propagated deletion for a file renamed
            and then unreadable; round 2 holds a gone baseline file row whose size and
            mtime (ns) both equal an untracked unreadable file's — identity, not name —
            and it then syncs as the rename it was. Blast radius: a coincidental exact
            (size, mtime) match holds one deletion until the unreadable file changes.
            Fixed here from the same family, pre-existing (W18-G): rows under an
            unreadable subdirectory are held where a detected parent rename puts them;
            W18-G's held ids join the keep-the-folder guard; held rows are no longer
            rename sources in `detectLocalDirMoves` (a copy of a locked folder paired as
            its rename and moved the Drive folder — reproduced, fixed).
            Tests: testing.md S014.1–S014.11. Verified by the coordinator after each
            round: `go build && go vet`, `gofmt -l`, `go test -count=1 ./...`, `-race`
            on scanner/reconcile/engine green on darwin; `GOOS=linux go build/vet`
            clean; 60×150 fuzz fails on seeds 1, 28, 45 only (S002).
            NOT closed, MANUAL §8 carries one row (→ S025): a file renamed AND touched
            before it became unreadable; a folder renamed while holding an unreadable
            subfolder and nothing readable beside it. WARN texts no longer promise "will
            not be deleted on either side". Recorded elsewhere: Windows sharing
            violations and macOS placeholder files still fail the whole scan (S006);
            `doctor` lists held paths under MissingLocal (S026).
