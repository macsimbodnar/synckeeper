// Package config loads and validates the synckeeper TOML configuration and
// resolves the per-OS config directory.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config mirrors config.toml. See docs/spec.md for the reference file.
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
	return dir, nil
}

// Default returns a Config populated with the documented defaults.
func Default() Config {
	machine, err := os.Hostname()
	if err != nil || machine == "" {
		machine = "unknown_machine"
	}
	machine = sanitizeMachineName(machine)
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
	if c.Engine.MachineName != sanitizeMachineName(c.Engine.MachineName) {
		return fmt.Errorf("engine.machine_name %q contains characters unsafe in filenames", c.Engine.MachineName)
	}
	return nil
}

// sanitizeMachineName keeps only characters safe in filenames on all
// supported platforms; the machine name is embedded in conflict filenames.
func sanitizeMachineName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		case r == '.', r == ' ':
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "unknown_machine"
	}
	return b.String()
}
