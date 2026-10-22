package native

import (
	"context"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukiyoz/resona/internal/client"
	w "github.com/tsukiyoz/resona/internal/nativewire"
	"github.com/tsukiyoz/resona/internal/noiseudp"
	"github.com/tsukiyoz/resona/internal/server"
)

func TestAdminBanClosesNativeSessionAndBlocksSameIdentityUntilRemoved(t *testing.T) {
	store := openTestResources(t, t.TempDir())
	claim, err := store.ProvisionOwnerClaim(false)
	if err != nil {
		t.Fatal(err)
	}
	owner := strings.Repeat("a", 64)
	if err := store.Claim(owner, claim); err != nil {
		t.Fatal(err)
	}
	key, err := noiseudp.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	s, err := server.Listen("127.0.0.1:0", server.Config{Name: "admin test", NoiseKey: key, Store: store, Channels: []w.Channel{{ID: 1, Name: "Lobby"}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("server shutdown timed out")
		}
	})
	pub, _ := noiseudp.PublicKey(key)
	profile := client.ServerProfile{Protocol: "resona-noise", Address: s.Addr().String(), Nickname: "member", ServerPublicKey: hex.EncodeToString(pub)}
	connector := Connector{IdentityPath: filepath.Join(t.TempDir(), "identity.key")}
	o := &observer{}
	connect := func() (client.RemoteConnection, error) {
		ctx, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		return connector.Connect(ctx, profile, "", o.update)
	}
	c, err := connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	uid := o.snapshot().IdentityUID
	if err := s.SetBan(ctx, owner, uid, "test", time.Minute, false, 0); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return c.(*connection).conn.Context().Err() != nil })
	if rejected, err := connect(); err == nil {
		_ = rejected.Close()
		t.Fatal("banned identity reconnected")
	}
	if err := s.SetBan(ctx, owner, uid, "", 0, true, 0); err != nil {
		t.Fatal(err)
	}
	again, err := connect()
	if err != nil {
		t.Fatal("unbanned identity could not reconnect", err)
	}
	_ = again.Close()
}
