package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/macsimbodnar/synckeeper/internal/auth"
	"github.com/macsimbodnar/synckeeper/internal/config"
	"github.com/macsimbodnar/synckeeper/internal/driveclient"
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
			// No instance lock. It used to be taken purely to force the daemon
			// stopped, because a running daemon held the refused token in memory
			// for its whole life — so the fix looked like it had not worked, and
			// the user had to uninstall the service to apply it. The daemon now
			// re-reads token.json when Google refuses a refresh (W19-3), so this
			// command is safe to run beside it, and it writes nothing but that
			// file (atomically, 0600).
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
			fmt.Println("Re-authenticated and verified. A running daemon picks the new token up on its next sync — no restart needed.")
			return nil
		},
	}
}
