package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/macsimbodnar/synckeeper/internal/auth"
	"github.com/macsimbodnar/synckeeper/internal/config"
	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/guards"
)

func newLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Re-authenticate with Google, refreshing the stored token",
		Long: "Runs the OAuth flow again and replaces the stored token. Use this when the token " +
			"has expired or been revoked (an `invalid_grant` error). Unlike `init`, it changes " +
			"nothing else — no folder setup, no reset of sync state.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			configDir, err := config.Dir()
			if err != nil {
				return err
			}
			// Holding the lock forces the daemon to be stopped first, which is
			// required anyway: a running daemon keeps the old token in memory
			// and won't use the new one until it restarts.
			// The lock forces the daemon stopped, which login needs anyway: a
			// running daemon holds the old token in memory. The lock error is
			// already actionable (guards).
			lock, err := guards.AcquireInstanceLock(configDir)
			if err != nil {
				return err
			}
			defer lock.Unlock()

			ts, err := auth.Login(ctx, configDir)
			if err != nil {
				return err
			}

			// Confirm the fresh token actually reaches Drive before declaring success.
			if client, cerr := driveclient.New(ctx, ts); cerr == nil {
				if _, verr := client.StartPageToken(ctx); verr != nil {
					fmt.Printf("Re-authenticated, but a test call to Drive failed: %v\n", verr)
					return nil
				}
			}
			fmt.Println("Re-authenticated and verified. Restart the daemon (`synckeeper watch`, or reinstall the service) to use the new token.")
			return nil
		},
	}
}
