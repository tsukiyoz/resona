package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tsukiyoz/resona/internal/server"
)

func newOwnerCommand(configDir string) *cobra.Command {
	owner := &cobra.Command{Use: "owner", Short: "Offline ownership provisioning (stop the server first)"}
	for _, reset := range []bool{false, true} {
		var store storeOptions
		cmd := &cobra.Command{Use: "init", Short: "Create a 24-hour claim; print the code once", Args: cobra.NoArgs}
		if reset {
			cmd.Use, cmd.Short = "reset-claim", "Replace an unused claim, never an existing owner"
		}
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			return store.withStore(func(store *server.Store) error {
				claim, err := store.ProvisionOwnerClaim(reset)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), claim)
				return err
			})
		}
		store.flags(cmd, configDir)
		owner.AddCommand(cmd)
	}
	return owner
}

func newAccountCommand(configDir string) *cobra.Command {
	var store storeOptions
	var username string
	var passwordStdin bool
	account := &cobra.Command{Use: "account", Short: "Offline account recovery (stop the server first)"}
	cmd := &cobra.Command{
		Use: "reset-password", Short: "Reset an account password from stdin and revoke sessions", Args: cobra.NoArgs,
		PreRunE: func(*cobra.Command, []string) error {
			if !passwordStdin {
				return errors.New("password reset requires --password-stdin")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			data, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 259))
			if err != nil {
				return err
			}
			defer clear(data)
			password := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
			return store.withStore(func(store *server.Store) error {
				uid, err := store.ResetAccountPassword(cmd.Context(), username, password)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "Password reset; UID %s\n", uid)
				return err
			})
		},
	}
	store.flags(cmd, configDir)
	cmd.Flags().StringVar(&username, "username", "", "registered account name")
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read the new password from stdin")
	_ = cmd.MarkFlagRequired("username")
	account.AddCommand(cmd)
	return account
}
