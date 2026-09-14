package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

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
	if version.Requested(os.Args[1:]) {
		fmt.Println("resona-server", version.Current())
		return nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	address := flag.String("listen", "127.0.0.1:9988", "UDP listen address")
	name := flag.String("name", "Resona", "server display name")
	noiseKeyPath := flag.String("noise-key", filepath.Join(configDir, "resona-server", "noise.key"), "Noise X25519 private key file")
	initKey := flag.Bool("init-key", false, "create a Noise identity and exit; never overwrite")
	channelsPath := flag.String("channels", "", "JSON channel array file (ID, Name, Description)")
	maxClients := flag.Int("max-clients", 64, "maximum simultaneous connections (1-64)")
	accessDir := flag.String("access-dir", filepath.Join(configDir, "resona-server", "access"), "persistent server ownership directory")
	initOwner := flag.Bool("init-owner", false, "offline: create a one-time owner claim (24h), print once, never overwrite")
	resetOwner := flag.Bool("reset-owner-claim", false, "offline: replace unused owner claim with a new 24h code; refuses an existing owner")
	showVersion := flag.Bool("version", false, "print build version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("resona-server", version.Current())
		return nil
	}
	if flag.NArg() != 0 {
		return errors.New("unexpected positional arguments")
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
	if *initOwner || *resetOwner {
		provision := server.InitOwnerClaim
		if *resetOwner {
			provision = server.ResetOwnerClaim
		}
		token, err := provision(*accessDir)
		if err != nil {
			return err
		}
		fmt.Println(token)
		return nil
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
		fmt.Printf("Noise server public key (X25519): %s\n", hex.EncodeToString(pub))
		return nil
	}
	noiseKey, err := os.ReadFile(*noiseKeyPath)
	if err != nil {
		return fmt.Errorf("load Noise identity (run --init-key first): %w", err)
	}
	if len(noiseKey) != 32 {
		return errors.New("invalid Noise identity file")
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
	ownership, err := server.OpenOwnership(*accessDir)
	if err != nil {
		return err
	}
	channelStore, err := server.OpenChannelStore(filepath.Join(*accessDir, "channels.json"), channels)
	if err != nil {
		return err
	}
	s, err := server.Listen(*address, server.Config{ChannelStore: channelStore, Ownership: ownership, NoiseKey: noiseKey, Name: *name, Password: os.Getenv("RESONA_SERVER_PASSWORD"), Channels: channels, MaxClients: *maxClients})
	if err != nil {
		return err
	}
	slog.Info("server listening", "address", s.Addr().String(), "version", version.Current().String())
	pub, _ := noiseudp.PublicKey(noiseKey)
	slog.Info("server identity", "public_key", hex.EncodeToString(pub))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return s.Serve(ctx)
}
func writeNew(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
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
