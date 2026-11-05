package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tsukiyoz/resona/internal/server"
)

func newPasswordCommand(configDir string) *cobra.Command {
	root := &cobra.Command{Use: "password", Short: "Offline connection password management (stop the server first)"}
	for _, reset := range []bool{false, true} {
		var store storeOptions
		var passwordStdin bool
		cmd := &cobra.Command{Use: "set", Short: "Set the connection password from stdin", Args: cobra.NoArgs}
		if reset {
			cmd.Use, cmd.Short = "reset", "Generate a connection password; print once after saving"
		} else {
			cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read the new password from stdin")
			cmd.PreRunE = func(*cobra.Command, []string) error {
				if !passwordStdin {
					return errors.New("password set requires --password-stdin")
				}
				return nil
			}
		}
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			password := ""
			if reset {
				password = rand.Text()
			} else {
				data, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1027))
				if err != nil {
					return err
				}
				defer clear(data)
				password = strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
			}
			return store.withStore(func(store *server.Store) error {
				if err := store.SetConnectionPassword(cmd.Context(), password); err != nil {
					return err
				}
				if reset {
					_, err := fmt.Fprintln(cmd.OutOrStdout(), password)
					return err
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "Server connection password updated")
				return err
			})
		}
		store.flags(cmd, configDir)
		root.AddCommand(cmd)
	}
	return root
}
