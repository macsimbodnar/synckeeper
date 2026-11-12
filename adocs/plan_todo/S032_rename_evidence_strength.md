id:         S032
goal:       How much evidence makes a folder "the same folder, renamed"? Today one
            paired child is enough. In `detectLocalDirMoves` (a LOCAL folder rename —
            pre-existing, confirmed on `5f61255` by S002's implementer, testing.md
            S002.28) even an EMPTY paired file suffices: `rm -r D`, a new unrelated `N`
            holding the same empty `__init__.py` plus a different `f.txt` plans
            `move_remote D→N` + `update_remote N/f.txt` — unrelated bytes over Drive's
            file, the old content only in Drive's version history. In S002's rule for a
            folder move the disk already shows, an empty file no longer counts, but one
            identical NON-EMPTY file does: a template-derived project dropped in at the
            renamed folder's name with the same LICENSE or `.gitignore` and a same-named
            but different README is accepted, and the README is uploaded over Drive's.
accepts:    DECIDED — DEC-012 (2026-11-12, by the coordinating agent under Max's
            delegation): more than half of the folder's non-empty files, as of the
            last sync, found in the candidate at the same relative path with their
            synced content; empty files count on neither side; both detectors. Below
            that: old folder deleted (bin), new folder new, nothing updated in place.
            Options that were on the table: (a) keep "one non-empty
            paired child", and apply the non-empty guard to `detectLocalDirMoves` too;
            (b) require a share of the folder's files to pair (say most of them);
            (c) inside a renamed folder, a same-named file whose bytes match neither
            side is always a conflict copy, never an update — evidence then only decides
            whether the folder keeps its Drive id. Then red-first tests for both rename
            detectors, specs §4.3/§16.7 (the fuzzer's identity-stability oracle encodes
            the current standard), MANUAL §6.
touches:    internal/reconcile, internal/engine (fuzz oracle), MANUAL.md, adocs/specs.md,
            adocs/testing.md
excludes:   S020's outcome (rename on Drive beats local delete) — only the evidence
            standard changes here.
closes:
paused_by:
author:
done:
