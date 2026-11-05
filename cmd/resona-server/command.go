package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	w "github.com/tsukiyoz/resona/internal/nativewire"
	"github.com/tsukiyoz/resona/internal/server"
	"github.com/tsukiyoz/resona/internal/version"
)

func newCommand() (*cobra.Command, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	configDir = filepath.Join(configDir, "resona-server")
	root := &cobra.Command{
		Use: "resona-server", Short: "Resona native voice server", Args: cobra.NoArgs,
		SilenceErrors: true, SilenceUsage: true, Version: version.Current().String(),
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	root.SetVersionTemplate("resona-server {{.Version}}\n")
	root.AddCommand(newServeCommand(configDir), newIdentityCommand(configDir),
		newOwnerCommand(configDir), newAdminCommand(configDir), newAccountCommand(configDir), newPasswordCommand(configDir),
		&cobra.Command{
			Use: "version", Short: "Print build information", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "resona-server", version.Current())
				return err
			},
		})
	return root, nil
}

type storeOptions struct {
	accessDir, chatDir, channelsPath string
	chatMessages, chatBytes          int64
	chatAge                          time.Duration
}

func (o *storeOptions) flags(cmd *cobra.Command, configDir string) {
	f := cmd.Flags()
	f.StringVar(&o.accessDir, "access-dir", filepath.Join(configDir, "access"), "server data access lock and import directory")
	f.StringVar(&o.chatDir, "chat-dir", "", "JetStream directory (default: access-dir/chat)")
	f.StringVar(&o.channelsPath, "channels", "", "initial channel JSON array (new stores only)")
	f.Int64Var(&o.chatMessages, "chat-max-messages", 200, "maximum retained messages per channel")
	f.Int64Var(&o.chatBytes, "chat-max-bytes", 64<<20, "maximum retained chat stream bytes")
	f.DurationVar(&o.chatAge, "chat-max-age", 7*24*time.Hour, "maximum retained chat message age")
}

// Every store command owns the same exclusive lock and closes the store before releasing it.
func (o storeOptions) withStore(run func(*server.Store) error) error {
	lock, err := server.LockAccess(o.accessDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	channels := []w.Channel{{ID: 1, Name: "Lobby"}, {ID: 2, Name: "Gaming"}}
	if o.channelsPath != "" {
		f, err := os.Open(o.channelsPath)
		if err != nil {
			return err
		}
		defer f.Close()
		d := json.NewDecoder(io.LimitReader(f, 65537))
		d.DisallowUnknownFields()
		if err := d.Decode(&channels); err != nil {
			return err
		}
		var extra any
		if err := d.Decode(&extra); err != io.EOF {
			return errors.New("invalid trailing channel configuration")
		}
	}
	if o.chatDir == "" {
		o.chatDir = filepath.Join(o.accessDir, "chat")
	}
	store, err := server.OpenStore(server.ChatConfig{
		Directory: o.chatDir, MaxMessagesPerChannel: o.chatMessages, MaxBytes: o.chatBytes, MaxAge: o.chatAge,
	}, o.accessDir, channels)
	if err != nil {
		return err
	}
	defer store.Close()
	return run(store)
}
