package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/macsimbodnar/synckeeper/internal/engine"
	"github.com/macsimbodnar/synckeeper/internal/service"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
	"github.com/macsimbodnar/synckeeper/internal/watch"
)

func newWatchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "watch",
		Short: "Run continuously, watching for local and remote changes",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			env, err := openAppEnv()
			if err != nil {
				return err
			}
			defer env.close()

			rootID, err := env.db.GetMeta(statedb.MetaRootFolderID)
			if errors.Is(err, statedb.ErrNotFound) {
				return errors.New("not initialized: run `synckeeper init` first")
			} else if err != nil {
				return err
			}
			client, err := env.driveClient(ctx)
			if err != nil {
				return err
			}

			w := &watch.Watcher{
				Eng: &engine.Engine{
					DB: env.db, Client: client, Cfg: env.cfg, SyncDir: env.syncDir,
					QuarantineDir: filepath.Join(env.configDir, "quarantine"), RootID: rootID,
				},
				Poll: time.Duration(env.cfg.Engine.PollIntervalSecs) * time.Second,
			}
			slog.Info("watching", "sync_dir", env.syncDir, "poll_interval", w.Poll)
			return w.Run(ctx)
		},
	}
}

func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service install|uninstall|status",
		Short: "Manage the login service that runs `synckeeper watch`",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var msg string
			var err error
			switch args[0] {
			case "install":
				bin, berr := os.Executable()
				if berr != nil {
					return berr
				}
				if bin, berr = filepath.EvalSymlinks(bin); berr != nil {
					return berr
				}
				msg, err = service.Install(bin)
			case "uninstall":
				msg, err = service.Uninstall()
			case "status":
				msg, err = serviceStatusText()
			default:
				return fmt.Errorf("unknown subcommand %q (want install, uninstall, or status)", args[0])
			}
			if err != nil {
				return err
			}
			fmt.Println(msg)
			return nil
		},
	}
	return cmd
}

func serviceStatusText() (string, error) {
	s, err := service.Status()
	if err != nil {
		return "", err
	}
	if !s.Installed {
		return "login service: not installed (run `synckeeper service install`)", nil
	}
	out := "login service: installed"
	if s.Enabled {
		out += ", starts at login"
	}
	if s.Running {
		out += ", running now"
	} else {
		out += ", not running"
	}
	if s.Detail != "" {
		out += "\n  " + s.Detail
	}
	return out, nil
}
