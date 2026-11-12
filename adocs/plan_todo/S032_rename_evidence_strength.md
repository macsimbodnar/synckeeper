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
accepts:    Max picks the standard first. Options named so far: (a) keep "one non-empty
            paired child", and apply the non-empty guard to `detectLocalDirMoves` too;
            (b) require a share of the folder's files to pair (say most of them);
            (c) inside a renamed folder, a same-named file whose bytes match neither
            side is always a conflict copy, never an update — evidence then only decides
            whether the folder keeps its Drive id. Then red-first tests for both rename
            detectors, specs §4.3/§16.7 (the fuzzer's identity-stability oracle encodes
            the current standard), MANUAL §6.
touches:    internal/reconcile, internal/engine (fuzz oracle), MANUAL.md, adocs/specs.md,
            adocs/testing.md
excludes:   Nothing is built before the standard is chosen.
closes:
paused_by:
author:
done:
