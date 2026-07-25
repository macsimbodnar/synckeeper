package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/macsimbodnar/synckeeper/internal/auth"
	"github.com/macsimbodnar/synckeeper/internal/config"
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
				Poll:          time.Duration(env.cfg.Engine.PollIntervalSecs) * time.Second,
				ControlSocket: filepath.Join(env.configDir, "control.sock"),
				ConfigDir:     env.configDir,
			}
			// Tighten the launchd log (0644 by default) to owner-only: it
			// records synced file names, and nobody else should read them.
			if err := service.RestrictLogToOwner(); err != nil {
				slog.Debug("could not restrict log file permissions", "err", err)
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
			if args[0] == "install" {
				// Confirm the daemon actually came up (or say why not) instead
				// of leaving the user to discover it from `status`/the log.
				time.Sleep(2 * time.Second) // let launchd start (or crash) it
				configDir, derr := config.Dir()
				reportServiceStartup(os.Stdout, service.Status, func() error {
					if derr != nil {
						return derr
					}
					_, _, e := auth.CredentialInfo(configDir)
					return e
				})
			}
			return nil
		},
	}
	return cmd
}

// reportServiceStartup checks whether the just-installed service came up and,
// if not, surfaces the likely cause — a missing credentials.json being the
// common one — rather than leaving the user to read the log.
func reportServiceStartup(w io.Writer, status func() (service.State, error), credOK func() error) {
	s, err := status()
	if err != nil {
		fmt.Fprintf(w, "\nCould not check whether the service started: %v\n", err)
		return
	}
	if s.Running {
		fmt.Fprintln(w, "\nService is running.")
		return
	}
	fmt.Fprintln(w, "\nInstalled, but the service is not running.")
	if cerr := credOK(); cerr != nil {
		fmt.Fprintf(w, "\nLikely cause — %v\n", cerr)
		fmt.Fprintln(w, "\nThe service runs `synckeeper watch`, which can't sign in by itself. "+
			"Stop it (`synckeeper service uninstall`), then run `synckeeper init` (first-time setup) "+
			"or `synckeeper login` (already set up), and reinstall.")
		return
	}
	if lp := service.LogPath(); lp != "" {
		fmt.Fprintf(w, "See the service log for why: %s\n", lp)
	} else {
		fmt.Fprintln(w, "Check `synckeeper status` and the service log for why.")
	}
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
