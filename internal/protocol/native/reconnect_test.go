package native

import (
	"context"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"github.com/tsukiyoz/resona/internal/client"
	w "github.com/tsukiyoz/resona/internal/nativewire"
	"github.com/tsukiyoz/resona/internal/noiseudp"
	"github.com/tsukiyoz/resona/internal/server"
)

type reconnectStore struct{ profile client.ServerProfile }

func (s reconnectStore) Load() ([]client.ServerProfile, error) {
	return []client.ServerProfile{s.profile}, nil
}
func (s reconnectStore) Save([]client.ServerProfile) error { return nil }

type controlConnection struct{ client.RemoteConnection }
type controlConnector struct{ Connector }

func (c controlConnector) Connect(ctx context.Context, p client.ServerProfile, password string, update func(client.RemoteState)) (client.RemoteConnection, error) {
	conn, err := c.Connector.Connect(ctx, p, password, update)
	if err != nil {
		return nil, err
	}
	// Exercise the real wire and identity without opening physical audio devices.
	return controlConnection{conn}, nil
}

func TestServiceReconnectsAfterRealServerRestart(t *testing.T) {
	key, err := noiseudp.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	cfg := server.Config{Name: "restart-test", NoiseKey: key, Channels: []w.Channel{{ID: 1, Name: "one"}, {ID: 2, Name: "two"}}}
	first, err := server.Listen("127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	address := first.Addr().String()
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- first.Serve(ctx) }()
	defer stop()
	pub, _ := noiseudp.PublicKey(key)
	profile := client.ServerProfile{ID: "test", Name: "test", Nickname: "test", Protocol: "resona-noise", Address: address, ServerPublicKey: hex.EncodeToString(pub)}
	s, err := client.NewWithConnector(reconnectStore{profile}, controlConnector{Connector{IdentityPath: filepath.Join(t.TempDir(), "identity.key")}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown()
	if _, err = s.ConnectServer("test", ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return s.GetSessionState().Mode == "connected" })
	before := s.GetSessionState()
	stop()
	<-done
	eventually(t, func() bool { return s.GetSessionState().Mode == "reconnecting" })
	second, err := server.Listen(address, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx2, stop2 := context.WithCancel(context.Background())
	done2 := make(chan error, 1)
	go func() { done2 <- second.Serve(ctx2) }()
	defer func() { stop2(); <-done2 }()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		after := s.GetSessionState()
		if after.Mode == "connected" && after.ID != before.ID {
			if after.IdentityUID != before.IdentityUID {
				t.Fatal("identity changed")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("not recovered: %+v", s.GetSessionState())
}
