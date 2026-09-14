package main

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tsukiyoz/resona/internal/client"
	w "github.com/tsukiyoz/resona/internal/nativewire"
	"github.com/tsukiyoz/resona/internal/noiseudp"
	"github.com/tsukiyoz/resona/internal/server"
)

func TestDefaultConnectorCreatesPersistentIdentityOnDemand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("APPDATA", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(dir, "resona")
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	router := defaultConnector()
	if _, err = os.Stat(filepath.Join(dir, "native-identity.key")); !os.IsNotExist(err) {
		t.Fatal("startup touched native identity")
	}
	key, err := noiseudp.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := noiseudp.PublicKey(key)
	s, err := server.Listen("127.0.0.1:0", server.Config{Name: "native independence", NoiseKey: key, Channels: []w.Channel{{ID: 1, Name: "Lobby"}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	defer func() { cancel(); <-done }()
	connectCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	c, err := router.Connect(connectCtx, client.ServerProfile{Protocol: "resona-noise", Address: s.Addr().String(), ServerPublicKey: hex.EncodeToString(pub), Nickname: "native"}, "", func(client.RemoteState) {})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "native-identity.key")); err != nil {
		t.Fatal("native identity not created on demand")
	}
}
