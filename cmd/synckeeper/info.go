package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/macsimbodnar/synckeeper/internal/auth"
	"github.com/macsimbodnar/synckeeper/internal/config"
	"github.com/macsimbodnar/synckeeper/internal/service"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
	"github.com/macsimbodnar/synckeeper/internal/status"
	"github.com/macsimbodnar/synckeeper/internal/trash"
)

func newInfoCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Show paths, configuration, identity, and version in one place",
		RunE: func(cmd *cobra.Command, args []string) error {
			v := gatherInfo()
			if asJSON {
				return printInfoJSON(os.Stdout, v)
			}
			printInfoHuman(os.Stdout, v)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the info as JSON")
	return cmd
}

// infoView is a static snapshot of the environment: paths, effective config,
// identity, and versions. It never makes a network call (see `account` for the
// live Google account) and never takes the instance lock, so it runs fine
// alongside the daemon and before `init`.
type infoView struct {
	version     string
	initialized bool // a state DB exists

	configDir     string
	configPath    string
	configLoaded  bool
	configErr     error
	statePath     string
	tokenPath     string
	credPath      string
	credExists    bool
	credMode      os.FileMode
	credLoose     bool // readable by group/world (W7-L6)
	socketPath    string
	quarantineDir string
	trashDest     string // where remote deletions go (W13); quarantine is the fallback
	logPath       string // "" when logs aren't file-based (Linux/Windows)

	syncDir     string
	driveFolder string
	rootID      string

	machineName string
	machineID   string

	oauthSource string
	oauthClient string
	oauthErr    error

	tokenPresent bool
	tokenMode    os.FileMode
	refreshToken bool
	tokenExpiry  int64 // unix; 0 when none/unknown
	tokenExpired bool

	pollSecs      int
	massThreshold float64
	retentionDays int
	ignore        []string

	items       int
	pending     int
	qFiles      int
	qBytes      int64
	daemonState string
}

func gatherInfo() infoView {
	v := infoView{version: version}

	configDir, err := config.Dir()
	if err != nil {
		v.configDir = fmt.Sprintf("(error resolving config dir: %v)", err)
		return v
	}
	v.configDir = configDir
	v.configPath = filepath.Join(configDir, "config.toml")
	v.statePath = statedb.Path(configDir)
	v.tokenPath = auth.TokenPath(configDir)
	v.credPath = filepath.Join(configDir, auth.CredentialsFile)
	v.socketPath = filepath.Join(configDir, "control.sock")
	v.quarantineDir = filepath.Join(configDir, "quarantine")
	v.trashDest = trash.Describe()
	v.logPath = service.LogPath()

	if fi, err := os.Stat(v.credPath); err == nil {
		v.credExists = true
		v.credMode = fi.Mode().Perm()
		v.credLoose = runtime.GOOS != "windows" && v.credMode&0o077 != 0
	}

	// OAuth client — resolvable without init (embedded default is always there).
	if src, id, cerr := auth.CredentialInfo(configDir); cerr != nil {
		v.oauthErr = cerr
	} else {
		v.oauthSource, v.oauthClient = string(src), id
	}

	// Token — present if the file exists; expiry/refresh best-effort.
	if fi, e := os.Stat(v.tokenPath); e == nil {
		v.tokenPresent = true
		v.tokenMode = fi.Mode().Perm()
		if tok, terr := auth.LoadToken(v.tokenPath); terr == nil {
			v.refreshToken = tok.RefreshToken != ""
			if !tok.Expiry.IsZero() {
				v.tokenExpiry = tok.Expiry.Unix()
				v.tokenExpired = tok.Expiry.Before(time.Now())
			}
		}
	}

	// Effective config — load if written, else fall back to defaults so the
	// paths/summary are still informative before `init`.
	cfg := config.Default()
	if _, e := os.Stat(v.configPath); e == nil {
		if loaded, lerr := config.Load(configDir); lerr == nil {
			cfg, v.configLoaded = loaded, true
		} else {
			v.configErr = lerr
		}
	}
	if sd, e := cfg.SyncDir(); e == nil {
		v.syncDir = sd
	}
	v.driveFolder = cfg.Drive.FolderName
	v.machineName = cfg.Engine.MachineName
	v.pollSecs = cfg.Engine.PollIntervalSecs
	v.massThreshold = cfg.Engine.MassDeleteThreshold
	v.retentionDays = cfg.Engine.QuarantineRetentionDays
	v.ignore = cfg.Engine.Ignore

	// Local state — read-only, only if a DB exists (no lock, no migration).
	if _, e := os.Stat(v.statePath); e == nil {
		func() {
			db, derr := statedb.OpenRead(v.statePath)
			if derr != nil {
				return
			}
			defer db.Close()
			v.initialized = true
			if rid, e := db.GetMeta(statedb.MetaRootFolderID); e == nil {
				v.rootID = rid
			}
			if mid, e := db.GetMeta(statedb.MetaMachineID); e == nil {
				v.machineID = mid
			}
			v.items, _ = db.ItemCount()
			v.pending, _ = db.PendingOpCount()
			ds, found := statedb.DaemonStatus{}, false
			if d, e := db.GetDaemonStatus(); e == nil {
				ds, found = d, true
			}
			v.daemonState = status.DaemonState(ds, found, daemonAlive(), time.Now(), stalenessWindow)
		}()
	}

	v.qFiles, v.qBytes = status.QuarantineUsage(v.quarantineDir)
	return v
}

func printInfoHuman(w io.Writer, v infoView) {
	fmt.Fprintf(w, "synckeeper %s\n\n", v.version)

	fmt.Fprintf(w, "config dir:    %s\n", v.configDir)
	fmt.Fprintf(w, "  config.toml  %s%s\n", v.configPath, note(!v.configLoaded, "not created — run `synckeeper init`"))
	fmt.Fprintf(w, "  state.db     %s%s\n", v.statePath, note(!v.initialized, "absent"))
	if v.tokenPresent {
		fmt.Fprintf(w, "  token.json   %s (present, %04o)\n", v.tokenPath, v.tokenMode)
	} else {
		fmt.Fprintf(w, "  token.json   %s (absent)\n", v.tokenPath)
	}
	switch {
	case !v.credExists:
		fmt.Fprintf(w, "  credentials  %s (absent — required, see below)\n", v.credPath)
	case v.credLoose:
		fmt.Fprintf(w, "  credentials  %s (present, %04o — readable by others; run: chmod 600 %s)\n", v.credPath, v.credMode, v.credPath)
	default:
		fmt.Fprintf(w, "  credentials  %s (present, %04o)\n", v.credPath, v.credMode)
	}
	fmt.Fprintf(w, "  control.sock %s\n", v.socketPath)
	fmt.Fprintf(w, "  quarantine   %s\n", v.quarantineDir)
	fmt.Fprintf(w, "  system bin   %s\n", v.trashDest)
	if v.logPath != "" {
		fmt.Fprintf(w, "  log          %s\n", v.logPath)
	}

	fmt.Fprintf(w, "\nsync dir:      %s%s\n", v.syncDir, note(!v.configLoaded, "default"))
	fmt.Fprintf(w, "drive folder:  %q%s\n", v.driveFolder, idSuffix(v.rootID))
	fmt.Fprintf(w, "machine:       %s%s\n", v.machineName, idSuffix(v.machineID))

	fmt.Fprintf(w, "\noauth client:  %s\n", orDash(v.oauthSource))
	if v.oauthErr != nil {
		fmt.Fprintf(w, "               (error resolving credentials: %v)\n", v.oauthErr)
	}
	if v.oauthClient != "" {
		fmt.Fprintf(w, "client id:     %s\n", v.oauthClient)
	}
	fmt.Fprintf(w, "token:         %s\n", tokenSummary(v))

	fmt.Fprintf(w, "\nconfig (effective%s):\n", note(!v.configLoaded, "defaults"))
	fmt.Fprintf(w, "  poll interval:    %ds\n", v.pollSecs)
	fmt.Fprintf(w, "  mass-delete:      %.2f\n", v.massThreshold)
	fmt.Fprintf(w, "  quarantine keep:  %dd\n", v.retentionDays)
	fmt.Fprintf(w, "  ignore:           %s\n", strings.Join(v.ignore, " "))

	fmt.Fprintf(w, "\nstate:\n")
	fmt.Fprintf(w, "  tracked items:    %d\n", v.items)
	fmt.Fprintf(w, "  pending ops:      %d\n", v.pending)
	fmt.Fprintf(w, "  quarantine:       %d files, %d bytes\n", v.qFiles, v.qBytes)
	if v.daemonState != "" {
		fmt.Fprintf(w, "  daemon:           %s\n", v.daemonState)
	}

	if !v.initialized {
		fmt.Fprintf(w, "\nNot initialized — run `synckeeper init`. The paths above are where files will live.\n")
	}
}

func printInfoJSON(w io.Writer, v infoView) error {
	out := map[string]any{
		"version":     v.version,
		"initialized": v.initialized,
		"config_dir":  v.configDir,
		"paths": map[string]any{
			"config_toml":      v.configPath,
			"state_db":         v.statePath,
			"token_json":       v.tokenPath,
			"credentials_json": v.credPath,
			"control_socket":   v.socketPath,
			"quarantine":       v.quarantineDir,
			"system_bin":       v.trashDest,
			"log":              v.logPath,
		},
		"credentials_present": v.credExists,
		"credentials_mode":    fmt.Sprintf("%04o", v.credMode),
		"credentials_loose":   v.credLoose,
		"sync_dir":            v.syncDir,
		"drive_folder":        v.driveFolder,
		"root_folder":         v.rootID,
		"machine_name":        v.machineName,
		"machine_id":          v.machineID,
		"oauth": map[string]any{
			"source":    v.oauthSource,
			"client_id": v.oauthClient,
		},
		"token": map[string]any{
			"present":       v.tokenPresent,
			"mode":          fmt.Sprintf("%04o", v.tokenMode),
			"refresh_token": v.refreshToken,
			"expiry":        v.tokenExpiry,
			"expired":       v.tokenExpired,
		},
		"config": map[string]any{
			"loaded":                    v.configLoaded,
			"poll_interval_secs":        v.pollSecs,
			"mass_delete_threshold":     v.massThreshold,
			"quarantine_retention_days": v.retentionDays,
			"ignore":                    v.ignore,
		},
		"state": map[string]any{
			"tracked_items":    v.items,
			"pending_ops":      v.pending,
			"quarantine_files": v.qFiles,
			"quarantine_bytes": v.qBytes,
			"daemon":           v.daemonState,
		},
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// tokenSummary renders the token line: presence, expiry, and refresh-token.
func tokenSummary(v infoView) string {
	if !v.tokenPresent {
		return "absent (run `synckeeper init`)"
	}
	var s string
	switch {
	case v.tokenExpiry == 0:
		s = "present (no expiry recorded)"
	case v.tokenExpired:
		s = fmt.Sprintf("present, expired %s (auto-refreshes on next use)", status.Ago(time.Now(), v.tokenExpiry))
	default:
		s = fmt.Sprintf("present, expires %s", status.Until(time.Now(), v.tokenExpiry))
	}
	if v.refreshToken {
		s += "; refresh token present"
	}
	return s
}

// note returns " (msg)" when cond holds, else "" — for inline path annotations.
func note(cond bool, msg string) string {
	if cond {
		return " (" + msg + ")"
	}
	return ""
}

// idSuffix returns " (id X)" when id is non-empty.
func idSuffix(id string) string {
	if id == "" {
		return ""
	}
	return " (id " + id + ")"
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
