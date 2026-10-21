package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
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
	w "github.com/tsukiyoz/resona/internal/nativewire"
	"github.com/tsukiyoz/resona/internal/noiseudp"
	"github.com/tsukiyoz/resona/internal/server"
	"github.com/tsukiyoz/resona/internal/version"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)).With("component", "resona-server"))
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cmd, err := newCommand()
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return cmd.ExecuteContext(ctx)
}

func newCommand() (*cobra.Command, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	root := &cobra.Command{
		Use: "resona-server", Short: "Resona native voice server", Args: cobra.NoArgs,
		SilenceErrors: true, SilenceUsage: true, Version: version.Current().String(),
	}
	root.SetVersionTemplate("resona-server {{.Version}}\n")
	flags := root.PersistentFlags()
	address := flags.String("listen", "127.0.0.1:9988", "UDP listen address")
	name := flags.String("name", "Resona", "server display name")
	noiseKeyPath := flags.String("noise-key", filepath.Join(configDir, "resona-server", "noise.key"), "Noise X25519 private key file")
	channelsPath := flags.String("channels", "", "initial channel JSON array (used only for a new store)")
	maxClients := flags.Int("max-clients", 64, "maximum simultaneous connections (1-64)")
	accessDir := flags.String("access-dir", filepath.Join(configDir, "resona-server", "access"), "server access lock and legacy import directory")
	chatDir := flags.String("chat-dir", "", "JetStream data directory for chat and resources (default: access-dir/chat)")
	chatMessages := flags.Int64("chat-max-messages", 200, "maximum retained messages per channel")
	chatBytes := flags.Int64("chat-max-bytes", 64<<20, "maximum retained chat stream bytes across channels")
	chatAge := flags.Duration("chat-max-age", 7*24*time.Hour, "maximum retained chat message age")
	// Keep existing service units and container entrypoints working.
	initKey := flags.Bool("init-key", false, "create a Noise identity and exit; never overwrite")
	initOwner := flags.Bool("init-owner", false, "offline: create a one-time owner claim")
	resetOwner := flags.Bool("reset-owner-claim", false, "offline: replace an unused owner claim")
	for _, flag := range []string{"init-key", "init-owner", "reset-owner-claim"} {
		_ = flags.MarkHidden(flag)
	}
	root.MarkFlagsMutuallyExclusive("init-key", "init-owner", "reset-owner-claim")
	execute := func(cmd *cobra.Command, mode string) error {
		if mode == "key" {
			*initKey = true
		}
		if mode == "owner" {
			*initOwner = true
		}
		if mode == "reset" {
			*resetOwner = true
		}
		if ((*initOwner || *resetOwner) && *initKey) || (*initOwner && *resetOwner) {
			return errors.New("choose one identity initialization mode")
		}
		if !*initKey {
			lock, err := server.LockAccess(*accessDir)
			if err != nil {
				return err
			}
			defer lock.Close()
		}
		if *initKey {
			key, e := noiseudp.GenerateKey()
			if e != nil {
				return e
			}
			if e = writeNew(*noiseKeyPath, key); e != nil {
				return e
			}
			pub, _ := noiseudp.PublicKey(key)
			fmt.Fprintf(cmd.OutOrStdout(), "Noise server public key (X25519): %s\n", hex.EncodeToString(pub))
			return nil
		}
		channels := []w.Channel{{ID: 1, Name: "Lobby"}, {ID: 2, Name: "Gaming"}}
		if *channelsPath != "" {
			f, err := os.Open(*channelsPath)
			if err != nil {
				return err
			}
			defer f.Close()
			d := json.NewDecoder(io.LimitReader(f, 65537))
			d.DisallowUnknownFields()
			if err = d.Decode(&channels); err != nil {
				return err
			}
			var extra any
			if err = d.Decode(&extra); err != io.EOF {
				return errors.New("invalid trailing channel configuration")
			}
		}
		if *chatDir == "" {
			*chatDir = filepath.Join(*accessDir, "chat")
		}
		var noiseKey []byte
		if !*initOwner && !*resetOwner {
			noiseKey, err = os.ReadFile(*noiseKeyPath)
			if err != nil {
				return fmt.Errorf("load Noise identity (run identity init first): %w", err)
			}
			if len(noiseKey) != 32 {
				return errors.New("invalid Noise identity file")
			}
		}
		store, err := server.OpenStore(server.ChatConfig{Directory: *chatDir, MaxMessagesPerChannel: *chatMessages, MaxBytes: *chatBytes, MaxAge: *chatAge}, *accessDir, channels)
		if err != nil {
			return err
		}
		defer store.Close()
		if *initOwner || *resetOwner {
			token, err := store.ProvisionOwnerClaim(*resetOwner)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), token)
			return nil
		}
		s, err := server.Listen(*address, server.Config{Store: store, NoiseKey: noiseKey, Name: *name, Password: os.Getenv("RESONA_SERVER_PASSWORD"), MaxClients: *maxClients})
		if err != nil {
			return err
		}
		slog.Info("server listening", "address", s.Addr().String(), "version", version.Current().String())
		pub, _ := noiseudp.PublicKey(noiseKey)
		slog.Info("server identity", "public_key", hex.EncodeToString(pub))
		return s.Serve(cmd.Context())
	}
	root.RunE = func(cmd *cobra.Command, _ []string) error { return execute(cmd, "serve") }
	serve := &cobra.Command{
		Use: "serve", Short: "Run the voice server", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return execute(cmd, "serve") },
	}
	identity := &cobra.Command{Use: "identity", Short: "Manage the Noise server identity"}
	identity.AddCommand(&cobra.Command{
		Use: "init", Short: "Create a private key and print its public key; never overwrite", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return execute(cmd, "key") },
	})
	owner := &cobra.Command{Use: "owner", Short: "Offline ownership provisioning (stop the server first)"}
	owner.AddCommand(&cobra.Command{
		Use: "init", Short: "Create a 24-hour claim; print the code once", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return execute(cmd, "owner") },
	},
		&cobra.Command{
			Use: "reset-claim", Short: "Replace an unused claim, never an existing owner", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return execute(cmd, "reset") },
		})
	root.AddCommand(serve, identity, owner, &cobra.Command{
		Use: "version", Short: "Print build information", Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), "resona-server", version.Current())
		},
	})
	return root, nil
}

func writeNew(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	err = errors.Join(writeErr, syncErr, closeErr)
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}
