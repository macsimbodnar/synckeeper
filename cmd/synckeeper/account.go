package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/macsimbodnar/synckeeper/internal/auth"
	"github.com/macsimbodnar/synckeeper/internal/config"
)

func newAccountCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "account",
		Short: "Show Google credential status",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, err := config.Dir()
			if err != nil {
				return err
			}
			path := auth.TokenPath(configDir)
			tok, err := auth.LoadToken(path)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Println("No token stored. Run `synckeeper init` to authenticate.")
					return nil
				}
				return err
			}
			fmt.Printf("token file:    %s\n", path)
			fmt.Printf("refresh token: %s\n", presentAbsent(tok.RefreshToken != ""))
			switch {
			case tok.Expiry.IsZero():
				fmt.Printf("access token:  present (no expiry recorded)\n")
			case tok.Expiry.Before(time.Now()):
				fmt.Printf("access token:  expired %s (auto-refreshes on next use)\n", ago(tok.Expiry.Unix()))
			default:
				fmt.Printf("access token:  valid, expires %s\n", until(tok.Expiry.Unix()))
			}
			fmt.Println("\nThe Google account email is not stored locally; see it in Drive's")
			fmt.Println("\"Security → Third-party apps\" list. (Showing it here is a phase-6 follow-up.)")
			return nil
		},
	}
}

func presentAbsent(b bool) string {
	if b {
		return "present"
	}
	return "absent"
}
