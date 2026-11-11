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
done:
