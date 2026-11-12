id:         S001
goal:       Finish the Linux port: the remaining L-items, then the hardware gates.
accepts:    L1, L2, L3, L4, L5, L7 land with their testing.md rows; L8 gates pass on
            Linux hardware — `go build && go vet && go test ./...` plus `-race`, the
            50k scale acceptance (`SYNCKEEPER_SCALE_FILES=50000`), and the 2 h soak
            (specs §16.9) run detached under `systemd-inhibit --what=sleep` against
            the production inotify backend. MANUAL and README claims updated only
            after L8 validated them.
touches:    internal/config, internal/auth, internal/service, internal/watch,
            cmd/synckeeper (the `--machine-name` flag trips the surface guard),
            MANUAL.md, README.md
excludes:   Windows (S006); the log-rotation question L2b, which is flagged but not
            yet a decision; any change to the settled conventional watching model
            (one inotify watch per directory, no watch budget, no config knob).
closes:
paused_by:
author:     Claude (agent), started 2026-09-03 at Max's direction
done:       2026-11-12 on `pop-os`, Claude (agent). L1–L6: testing.md W7-L1..W7-L6 (L6
            2026-07-25, the rest 2026-09-03). L8 on Linux hardware, inotify: build/vet/test green at `5c1a807`;
            `-race` red there (tui, bubbletea kill path) → blocking child S027 → green at
            `43c8c89`; 50k scale 0 of 500 dirs unwatchable (756 s); 2 h soak detached under
            `systemd-inhibit --what=sleep`, converged on 16,419 files (`1019cae`, 7206.71 s).
            Rows: testing.md S001 L8.1–L8.5. L7: MANUAL §9 (validation bullet cites L8;
            mixed case-sensitivity bullet), README status line; the other L7 targets had
            landed early (W18-I D2). Deviations: soak ran with `XDG_DATA_HOME` in a scratch
            dir — the watch tests reach the real bin (S029; 16,455 items in 2 h); `-count=1`
            on every gate; logs in the session scratchpad, not the repo. Found on the way:
            S016 (F11 confirmed and fixed here), S027, S028, S029.
