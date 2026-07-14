package main

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/macsimbodnar/synckeeper/internal/config"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// errNotInitialized is returned by openReadEnv when no config exists yet.
var errNotInitialized = errors.New("not initialized: run `synckeeper init` first")

// readEnv is the plumbing for read-only monitoring commands (status,
// activity, config, account). Unlike appEnv it does NOT take the instance
// lock, so it runs fine while the watch daemon holds it. It only reads —
// SQLite WAL allows concurrent readers alongside the daemon's single writer.
type readEnv struct {
	configDir string
	cfg       config.Config
	syncDir   string
	db        *statedb.DB
}

func openReadEnv() (*readEnv, error) {
	configDir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(configDir, "config.toml")); os.IsNotExist(err) {
		return nil, errNotInitialized
	}
	cfg, err := config.Load(configDir)
	if err != nil {
		return nil, err
	}
	syncDir, err := cfg.SyncDir()
	if err != nil {
		return nil, err
	}
	db, err := statedb.Open(statedb.Path(configDir))
	if err != nil {
		return nil, err
	}
	return &readEnv{configDir: configDir, cfg: cfg, syncDir: syncDir, db: db}, nil
}

func (e *readEnv) close() { e.db.Close() }
