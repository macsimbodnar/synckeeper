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
done:
