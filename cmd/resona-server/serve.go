package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tsukiyoz/resona/internal/noiseudp"
	"github.com/tsukiyoz/resona/internal/server"
	"github.com/tsukiyoz/resona/internal/version"
)

func newServeCommand(configDir string) *cobra.Command {
	var store storeOptions
	var address, name, keyPath, adminListen, adminOrigin string
	var maxClients, voiceQueueSlots int
	cmd := &cobra.Command{
		Use: "serve", Short: "Run the voice server", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if os.Getenv("RESONA_SERVER_PASSWORD") != "" || os.Getenv("RESONA_SERVER_PASSWORD_FILE") != "" {
				return errors.New("connection password environment configuration is unsupported; use password set --password-stdin with the same access directory")
			}
			if voiceQueueSlots < 1 || voiceQueueSlots > server.MaxVoiceQueueSlots {
				return fmt.Errorf("--voice-queue-slots must be between 1 and %d", server.MaxVoiceQueueSlots)
			}
			key, err := os.ReadFile(keyPath)
			if err != nil {
				return fmt.Errorf("load Noise identity (run identity init first): %w", err)
			}
			if len(key) != 32 {
				return errors.New("invalid Noise identity file")
			}
			return store.withStore(func(store *server.Store) error {
				s, err := server.Listen(address, server.Config{
					Store: store, NoiseKey: key,
					Name: name, MaxClients: maxClients,
					VoiceQueueSlots: voiceQueueSlots,
				})
				if err != nil {
					return err
				}
				slog.Info("server listening", "address", s.Addr().String(), "version", version.Current().String(), "voice_queue_slots", voiceQueueSlots)
				pub, _ := noiseudp.PublicKey(key)
				slog.Info("server identity", "public_key", hex.EncodeToString(pub))
				return s.ServeWithAdmin(cmd.Context(), adminListen, adminOrigin)
			})
		},
	}
	store.flags(cmd, configDir)
	f := cmd.Flags()
	f.StringVar(&address, "listen", "127.0.0.1:9988", "UDP listen address")
	f.StringVar(&name, "name", "Resona", "server display name")
	f.StringVar(&keyPath, "noise-key", filepath.Join(configDir, "noise.key"), "Noise X25519 private key file")
	f.IntVar(&maxClients, "max-clients", 64, "maximum simultaneous connections (1-64)")
	f.IntVar(&voiceQueueSlots, "voice-queue-slots", server.DefaultVoiceQueueSlots, "pending voice packets per recipient (1-256; restart to apply)")
	f.StringVar(&adminListen, "admin-listen", "", "optional HTTP management bind address")
	f.StringVar(&adminOrigin, "admin-origin", "", "browser origin; public origins require HTTPS")
	return cmd
}
