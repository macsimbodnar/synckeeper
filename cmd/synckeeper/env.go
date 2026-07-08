package main

import (
	"context"

	"github.com/macsimbodnar/synckeeper/internal/auth"
	"github.com/macsimbodnar/synckeeper/internal/config"
	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/guards"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// appEnv is the shared plumbing of every state-touching command: config
// dir, loaded config, open DB, and the held instance lock.
type appEnv struct {
	configDir string
	cfg       config.Config
	syncDir   string
	db        *statedb.DB
	lock      *guards.InstanceLock
}

func openAppEnv() (*appEnv, error) {
	configDir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	lock, err := guards.AcquireInstanceLock(configDir)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(configDir)
	if err != nil {
		lock.Unlock()
		return nil, err
	}
	syncDir, err := cfg.SyncDir()
	if err != nil {
		lock.Unlock()
		return nil, err
	}
	db, err := statedb.Open(statedb.Path(configDir))
	if err != nil {
		lock.Unlock()
		return nil, err
	}
	return &appEnv{configDir: configDir, cfg: cfg, syncDir: syncDir, db: db, lock: lock}, nil
}

func (e *appEnv) close() {
	e.db.Close()
	e.lock.Unlock()
}

// driveClient authenticates from the stored token and returns a real client.
func (e *appEnv) driveClient(ctx context.Context) (driveclient.Client, error) {
	ts, err := auth.TokenSource(ctx, e.configDir)
	if err != nil {
		return nil, err
	}
	return driveclient.New(ctx, ts)
}
