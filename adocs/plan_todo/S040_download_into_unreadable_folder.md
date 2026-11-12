id:         S040
goal:       A new Drive file inside a local folder that cannot be read fails its
            download every cycle, forever. PRE-EXISTING (same at `14ed882`); seen by
            both independent checks of S038 (2026-11-12, probe `TestZZUnreadable/plain`
            — no case rename involved). Nothing is lost; the cycle never comes back
            clean, so every safeguard gated on a clean cycle stays off (S022's family).
accepts:    Red-first, engine level: Drive adds a file under a folder that is
            unreadable here → no failed action; the file is held and reported like the
            folder's other entries (the unreadable skip, §5), and arrives by itself
            once the folder is readable again. The same for a new Drive subfolder
            under it. MANUAL §6/§8; specs §5; testing.md.
touches:    internal/reconcile (pass 3 under an unreadable ancestor), internal/engine
            (`expandUnreadable`), tests, MANUAL.md, adocs/specs.md, adocs/testing.md
excludes:   S022 (what a failed cycle gates), S003 (where skips are shown).
closes:
paused_by:
author:
done:
