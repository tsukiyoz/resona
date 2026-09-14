package native

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/tsukiyoz/resona/internal/client"
)

// Opt-in smoke test for our local Compose instance, never the user's 服务器 server.
func TestDockerNoiseSmoke(t *testing.T) {
	address := os.Getenv("RESONA_TEST_NOISE_ADDRESS")
	if address == "" {
		t.Skip("set RESONA_TEST_NOISE_ADDRESS and RESONA_TEST_NOISE_PUBLIC_KEY")
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		t.Fatal("container smoke test requires a loopback address")
	}
	p := client.ServerProfile{Protocol: "resona-noise", Address: address, Nickname: "container-smoke-A", ServerPublicKey: os.Getenv("RESONA_TEST_NOISE_PUBLIC_KEY")}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connect := func(profile client.ServerProfile) (*connection, *observer) {
		t.Helper()
		o := &observer{}
		r, err := (Connector{}).Connect(ctx, profile, os.Getenv("RESONA_TEST_NOISE_PASSWORD"), o.update)
		if err != nil {
			t.Fatal(err)
		}
		c := r.(*connection)
		t.Cleanup(func() { _ = c.Close() })
		return c, o
	}
	a, _ := connect(p)
	p.Nickname = "container-smoke-B"
	_, b := connect(p)
	if err := a.SendChannelMessage(ctx, "1", "container smoke test"); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.messages) == 1 && b.messages[0].Text == "container smoke test"
	})
	if err := a.MoveChannel(ctx, "2"); err != nil {
		t.Fatal(err)
	}
}
