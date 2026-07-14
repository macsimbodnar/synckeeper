package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/macsimbodnar/synckeeper/internal/config"
	"github.com/macsimbodnar/synckeeper/internal/control"
)

// errNoDaemon is returned by control commands when the daemon isn't running.
var errNoDaemon = errors.New("the watch daemon is not running (start it with `synckeeper watch` or `synckeeper service install`)")

func controlSocketPath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "control.sock"), nil
}

// callDaemon sends a request to the daemon. running is false when no daemon
// is listening; err is set only on a real protocol/transport failure.
func callDaemon(req control.Request) (resp control.Response, running bool, err error) {
	sock, err := controlSocketPath()
	if err != nil {
		return control.Response{}, false, err
	}
	resp, err = control.Call(sock, req)
	if control.IsNotRunning(err) {
		return control.Response{}, false, nil
	}
	if err != nil {
		return control.Response{}, true, err
	}
	return resp, true, nil
}

// daemonAlive reports whether the daemon answers a ping.
func daemonAlive() bool {
	_, running, _ := callDaemon(control.Request{Cmd: control.CmdPing})
	return running
}

// simpleControl runs a control command that just needs an ok/error and prints
// successMsg on success.
func simpleControl(req control.Request, successMsg string) error {
	resp, running, err := callDaemon(req)
	if err != nil {
		return err
	}
	if !running {
		return errNoDaemon
	}
	if !resp.OK {
		return errors.New(resp.Error)
	}
	fmt.Println(successMsg)
	return nil
}

func newPauseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pause",
		Short: "Suspend automatic syncing in the running daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return simpleControl(control.Request{Cmd: control.CmdPause},
				"Paused. Automatic syncing is suspended; run `synckeeper resume` to continue.")
		},
	}
}

func newResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Resume automatic syncing in the running daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return simpleControl(control.Request{Cmd: control.CmdResume},
				"Resumed. Automatic syncing is active.")
		},
	}
}

func newReloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "Re-read config.toml in the running daemon without restarting",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, running, err := callDaemon(control.Request{Cmd: control.CmdReload})
			if err != nil {
				return err
			}
			if !running {
				return errNoDaemon
			}
			if !resp.OK {
				return errors.New(resp.Error)
			}
			var d struct {
				NeedsRestart []string `json:"needs_restart"`
			}
			json.Unmarshal(resp.Data, &d)
			fmt.Println("Config reloaded.")
			if len(d.NeedsRestart) > 0 {
				fmt.Printf("These changes need a daemon restart to take effect: %s\n", strings.Join(d.NeedsRestart, ", "))
			}
			return nil
		},
	}
}
