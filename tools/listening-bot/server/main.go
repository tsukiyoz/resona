// listening-server provides only an isolated server for the Rust listening tool.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/signal"

	w "github.com/tsukiyoz/resona/internal/nativewire"
	"github.com/tsukiyoz/resona/internal/noiseudp"
	"github.com/tsukiyoz/resona/internal/server"
)

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); cancel() }()
	key, err := noiseudp.GenerateKey()
	if err != nil {
		return err
	}
	channels := []w.Channel{
		{ID: 1, Name: "01 原始人声与底噪", Bitrate: 32000},
		{ID: 2, Name: "02 SpeexDSP ANS 中等", Bitrate: 32000},
		{ID: 3, Name: "03 WebRTC NS 中等", Bitrate: 32000},
		{ID: 4, Name: "04 AGC 三人轮流（轻声 / 正常 / 较响）", Bitrate: 32000},
	}
	s, err := server.Listen("127.0.0.1:0", server.Config{Name: "Resona 本地音质试听", NoiseKey: key, Channels: channels, MaxClients: 8})
	if err != nil {
		return err
	}
	pub, _ := noiseudp.PublicKey(key)
	if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"address": s.Addr().String(), "publicKey": hex.EncodeToString(pub)}); err != nil {
		cancel()
		_ = s.Serve(ctx)
		return err
	}
	return s.Serve(ctx)
}

func main() {
	if err := run(); err != nil {
		// No user credentials or chat/audio payloads are accepted by this fixture.
		os.Stderr.WriteString("local listening server failed\n")
		os.Exit(1)
	}
}
