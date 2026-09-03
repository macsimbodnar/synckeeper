// Package config loads and validates the synckeeper TOML configuration and
// resolves the per-OS config directory.
package config

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config mirrors config.toml. See adocs/specs.md for the reference file.
type Config struct {
	Drive  DriveConfig  `toml:"drive"`
	Local  LocalConfig  `toml:"local"`
	Engine EngineConfig `toml:"engine"`
}

type DriveConfig struct {
	FolderName string `toml:"folder_name"`
}

type LocalConfig struct {
	SyncDir string `toml:"sync_dir"`
}

type EngineConfig struct {
	PollIntervalSecs        int      `toml:"poll_interval_secs"`
	MassDeleteThreshold     float64  `toml:"mass_delete_threshold"`
	MachineName             string   `toml:"machine_name"`
	QuarantineRetentionDays int      `toml:"quarantine_retention_days"`
	Ignore                  []string `toml:"ignore"`
}

// Dir returns the synckeeper config directory, creating it if needed.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	dir := filepath.Join(base, "synckeeper")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	tightenDir(dir)
	return dir, nil
}

// tightenDir restores 0700 on a config dir that already existed. MkdirAll's
// mode applies only when it creates the directory, and MANUAL §5 tells users
// to place credentials.json here *before* the first run — so a hand-created
// dir keeps the user's umask (0755/0775 observed) and exposes the OAuth
// client secret and the quarantine to other local users. Best-effort: a chmod
// we are not allowed to make is a warning, never a failed command. No-op on
// Windows, where the permission bits are not meaningful. (W7-L6.)
func tightenDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm()&0o077 == 0 {
		return
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		slog.Warn("config dir is accessible to other users and could not be tightened",
			"dir", dir, "perm", fmt.Sprintf("%04o", info.Mode().Perm()), "err", err)
		return
	}
	slog.Debug("tightened config dir permissions", "dir", dir, "was", fmt.Sprintf("%04o", info.Mode().Perm()))
}

// DefaultMachineName returns the sanitized OS hostname and whether it is
// usable. ok is false when the hostname is unavailable, empty, or sanitizes to
// nothing — the cases where the name would silently become "unknown_machine";
// the caller (`init`, W7-L1) needs to tell those apart so it can prompt without
// a misleading default and say what happened.
func DefaultMachineName() (string, bool) {
	h, err := os.Hostname()
	if err != nil {
		return unknownMachineName, false
	}
	if s := sanitizeName(h); s != "" {
		return s, true
	}
	return unknownMachineName, false
}

// Default returns a Config populated with the documented defaults.
func Default() Config {
	machine, _ := DefaultMachineName()
	return Config{
		Drive: DriveConfig{FolderName: "Synckeeper"},
		Local: LocalConfig{SyncDir: "~/Synckeeper"},
		Engine: EngineConfig{
			PollIntervalSecs:        45,
			MassDeleteThreshold:     0.25,
			MachineName:             machine,
			QuarantineRetentionDays: 30,
			Ignore:                  []string{"*.tmp", "~$*", ".DS_Store", "Thumbs.db", "*.swp", ".synckeeper*"},
		},
	}
}

// Load reads and validates <dir>/config.toml. Missing keys fall back to
// defaults; unknown keys are rejected to catch typos.
func Load(dir string) (Config, error) {
	cfg := Default()
	path := filepath.Join(dir, "config.toml")
	meta, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return Config{}, fmt.Errorf("%s: unknown key %q", path, undecoded[0].String())
	}
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// WriteDefault writes a default config.toml into dir unless one exists.
// It returns the path and whether a new file was created.
func WriteDefault(dir string) (string, bool, error) {
	path := filepath.Join(dir, "config.toml")
	if _, err := os.Stat(path); err == nil {
		return path, false, nil
	} else if !os.IsNotExist(err) {
		return path, false, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return path, false, err
	}
	defer f.Close()
	if err := toml.NewEncoder(f).Encode(Default()); err != nil {
		return path, false, fmt.Errorf("write %s: %w", path, err)
	}
	return path, true, nil
}

// SetMachineName rewrites <dir>/config.toml with engine.machine_name set to
// name, atomically and 0600 like the original. `init` prompts for the name
// (W7-L1) and needs somewhere to put the answer; WriteDefault only ever writes
// a file that does not exist yet.
//
// Re-encoding drops comments from a hand-edited config.toml. Accepted: the
// file WriteDefault produces is machine-generated and has none, and on a
// re-run the caller only writes when the user actually changed the name.
func SetMachineName(dir string, name string) error {
	cfg, err := Load(dir)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "config.toml")
	cfg.Engine.MachineName = name
	if err := cfg.validate(); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// SyncDir returns the sync directory with ~ expanded to the user home,
// as an absolute path.
func (c Config) SyncDir() (string, error) {
	p := c.Local.SyncDir
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		p = filepath.Join(home, p[1:])
	}
	return filepath.Abs(p)
}

func (c Config) validate() error {
	if c.Drive.FolderName == "" {
		return fmt.Errorf("drive.folder_name must not be empty")
	}
	if c.Local.SyncDir == "" {
		return fmt.Errorf("local.sync_dir must not be empty")
	}
	if c.Engine.PollIntervalSecs <= 0 {
		return fmt.Errorf("engine.poll_interval_secs must be positive")
	}
	if c.Engine.MassDeleteThreshold <= 0 || c.Engine.MassDeleteThreshold > 1 {
		return fmt.Errorf("engine.mass_delete_threshold must be in (0, 1]")
	}
	if c.Engine.QuarantineRetentionDays <= 0 {
		return fmt.Errorf("engine.quarantine_retention_days must be positive")
	}
	if c.Engine.MachineName == "" {
		return fmt.Errorf("engine.machine_name must not be empty")
	}
	if c.Engine.MachineName != SanitizeMachineName(c.Engine.MachineName) {
		return fmt.Errorf("engine.machine_name %q contains characters unsafe in filenames", c.Engine.MachineName)
	}
	return nil
}

// unknownMachineName is the name used when nothing better is available. It is
// a real, valid name: conflict copies must always be attributable to *some*
// machine, even a badly configured one.
const unknownMachineName = "unknown_machine"

// SanitizeMachineName keeps only characters safe in filenames on all
// supported platforms; the machine name is embedded in conflict filenames.
// A name that sanitizes to nothing becomes unknownMachineName. Idempotent, so
// a sanitized name always passes validate().
func SanitizeMachineName(name string) string {
	if s := sanitizeName(name); s != "" {
		return s
	}
	return unknownMachineName
}

// sanitizeName is SanitizeMachineName without the fallback, so callers that
// must know "there was nothing usable here" can see the empty result.
func sanitizeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		case r == '.', r == ' ':
			b.WriteRune('_')
		}
	}
	return b.String()
}
