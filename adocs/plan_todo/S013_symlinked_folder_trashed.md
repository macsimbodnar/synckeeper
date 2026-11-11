id:         S013
goal:       A tracked folder replaced by a symlink has its Drive folder trashed and is
            removed from other machines. `internal/scanner/scanner.go:68-71` reports
            the symlink as a skip and drops the path from the snapshot; only
            `Skip.Unreadable` is held harmless (`internal/engine/engine.go:358-378`).
            Found by audit 2026-11-11_adversarial (F07, Medium).
accepts:    `TestAudit20261111_F07_FolderReplacedBySymlinkIsTrashedOnDrive` passes
            ungated: 3 of 3 files left on Drive and on machine b, the skip reported.
            MANUAL §8 row leaves the list in the fix's commit; testing.md row lands
            with the fix.
touches:    internal/scanner, internal/engine, MANUAL.md, adocs/specs.md §5,
            adocs/testing.md
excludes:   Following symlinks. Surfacing skips under the daemon (S003).
closes:     2026-11-11_adversarial-F07
paused_by:
author:
done:
