package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/macsimbodnar/synckeeper/internal/auth"
	"github.com/macsimbodnar/synckeeper/internal/config"
	"github.com/macsimbodnar/synckeeper/internal/driveclient"
)

func newAccountCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "account",
		Short: "Show Google credential and account status",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, err := config.Dir()
			if err != nil {
				return err
			}
			if src, clientID, cerr := auth.CredentialInfo(configDir); cerr != nil {
				fmt.Printf("oauth client:  error resolving credentials: %v\n", cerr)
			} else {
				fmt.Printf("oauth client:  %s\n", src)
				fmt.Printf("client id:     %s\n", clientID)
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

			// One about.get to name the signed-in account. Best-effort: a
			// bounded timeout keeps `account` responsive offline, and any
			// failure prints a hint rather than failing the command.
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			about, aerr := accountIdentity(ctx, configDir)
			printAccountIdentity(os.Stdout, about, aerr)
			return nil
		},
	}
}

// accountIdentity fetches the signed-in Google account's identity via a single
// about.get. It builds the client from the stored token; every failure path
// (no token, offline, API error) returns an error the caller renders as a
// note — `account` must still report local status when Drive is unreachable.
func accountIdentity(ctx context.Context, configDir string) (driveclient.About, error) {
	ts, err := auth.TokenSource(ctx, configDir)
	if err != nil {
		return driveclient.About{}, err
	}
	client, err := driveclient.New(ctx, ts)
	if err != nil {
		return driveclient.About{}, err
	}
	return client.About(ctx)
}

// printAccountIdentity renders the about.get result. On error it prints an
// offline note; on success the account email (with display name when present).
func printAccountIdentity(w io.Writer, a driveclient.About, err error) {
	switch {
	case err != nil:
		fmt.Fprintf(w, "google account: unavailable (%v)\n", err)
	case a.Email == "":
		fmt.Fprintln(w, "google account: (Drive returned no account email)")
	case a.DisplayName != "":
		fmt.Fprintf(w, "google account: %s <%s>\n", a.DisplayName, a.Email)
	default:
		fmt.Fprintf(w, "google account: %s\n", a.Email)
	}
}

func presentAbsent(b bool) string {
	if b {
		return "present"
	}
	return "absent"
}
