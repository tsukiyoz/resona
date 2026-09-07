//go:build integration

package ts3

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tsukiyoz/resona/internal/client"
)

// Explicitly opt in against an authorized server. No queries, messages, voice,
// subscription changes or administration commands are sent by this probe.
func TestLiveReadOnlySession(t *testing.T) {
	address := os.Getenv("RESONA_TS_ADDRESS")
	if address == "" {
		t.Skip("RESONA_TS_ADDRESS is not set")
	}
	path := os.Getenv("RESONA_TS_IDENTITY")
	if path == "" {
		path = filepath.Join(t.TempDir(), "identity.key")
	}
	c := New(path)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var mu sync.Mutex
	var latest client.RemoteState
	conn, err := c.Connect(ctx, client.ServerProfile{Address: address, Nickname: "Resona-Verify"}, os.Getenv("RESONA_TS_PASSWORD"), func(s client.RemoteState) {
		mu.Lock()
		latest = s
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	mu.Lock()
	s := latest
	mu.Unlock()
	if s.Closed || s.SelfID == "" || s.ChannelID == "" || len(s.Channels) == 0 || len(s.Users) == 0 {
		t.Fatalf("incomplete initial state: channels=%d users=%d self=%t channel=%t closed=%t", len(s.Channels), len(s.Users), s.SelfID != "", s.ChannelID != "", s.Closed)
	}
	id, err := loadIdentity(context.Background(), path)
	if err != nil || identityUID(id) != s.IdentityUID {
		t.Fatal("identity persistence mismatch")
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("connected; channels=%d visible_users=%d; own channel and persistent identity verified; disconnected", len(s.Channels), len(s.Users))
}
