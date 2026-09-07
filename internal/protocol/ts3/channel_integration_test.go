//go:build integration

package ts3

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tsukiyoz/resona/internal/client"
)

type integrationProfiles []client.ServerProfile

func (p integrationProfiles) Load() ([]client.ServerProfile, error) { return p, nil }
func (p integrationProfiles) Save([]client.ServerProfile) error     { return nil }

func TestLiveDedicatedChannelRemoved(t *testing.T) {
	address, target, name, identity := os.Getenv("RESONA_TS_ADDRESS"), os.Getenv("RESONA_TS_TEST_CHANNEL_ID"), os.Getenv("RESONA_TS_TEST_CHANNEL_NAME"), os.Getenv("RESONA_TS_IDENTITY")
	if address == "" || target == "" || name == "" || identity == "" {
		t.Skip("authorized identity and dedicated test channel environment are required")
	}
	if !strings.HasPrefix(name, "Resona Test ") || !validID(target) {
		t.Fatal("dedicated Resona test channel required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var mu sync.Mutex
	var latest client.RemoteState
	connection, err := New(identity).Connect(ctx, client.ServerProfile{Address: address, Nickname: "Resona-CleanupVerify"}, os.Getenv("RESONA_TS_PASSWORD"), func(state client.RemoteState) {
		mu.Lock()
		latest = state
		mu.Unlock()
	})
	if err != nil {
		t.Fatal("cleanup verification connection failed")
	}
	defer connection.Close()
	mu.Lock()
	defer mu.Unlock()
	for _, channel := range latest.Channels {
		if channel.ID == target || channel.Name == name {
			t.Fatal("temporary test channel is still present")
		}
	}
	t.Log("dedicated temporary channel ID and name absent from initial server state")
}

// The caller must create and keep its own temporary test channel alive first.
// This probe only enters that channel and disconnects; it does not return to or
// exercise any existing activity channel, nor create/delete server resources.
func TestLiveDedicatedChannelMove(t *testing.T) {
	address, target, name, identity := os.Getenv("RESONA_TS_ADDRESS"), os.Getenv("RESONA_TS_TEST_CHANNEL_ID"), os.Getenv("RESONA_TS_TEST_CHANNEL_NAME"), os.Getenv("RESONA_TS_IDENTITY")
	if address == "" || target == "" || name == "" || identity == "" {
		t.Skip("authorized identity and dedicated test channel environment are required")
	}
	if !strings.HasPrefix(name, "Resona Test ") || !validID(target) {
		t.Fatal("dedicated Resona test channel required")
	}
	service, err := client.NewWithConnector(integrationProfiles{{ID: "integration", Name: "Integration", Address: address, Nickname: "Resona-Verify"}}, New(identity))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Shutdown()
	if _, err := service.ConnectServer("integration", os.Getenv("RESONA_TS_PASSWORD")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	wait := func(done func(client.Workspace) bool) client.Workspace {
		t.Helper()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			s, err := service.GetWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			if s.Session.Mode == "failed" || s.Session.Error != "" {
				t.Fatalf("session failed: %s", s.Session.Error)
			}
			if done(s) {
				return s
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				t.Fatal("session operation timeout")
			}
		}
	}
	s := wait(func(s client.Workspace) bool { return s.Session.Mode == "connected" })
	found := false
	for _, ch := range s.Channels {
		if ch.ID == target && ch.Name == name && !ch.PasswordRequired {
			found = true
		}
	}
	if !found {
		t.Fatal("dedicated channel ID/name absent or protected; refusing to move")
	}
	if s.Session.ChannelID == target {
		t.Fatal("already in target; cannot verify a channel transition")
	}
	if _, err := service.SelectChannel(target); err != nil {
		t.Fatal(err)
	}
	s = wait(func(s client.Workspace) bool {
		return s.Session.ChannelID == target && s.Session.SwitchingChannelID == ""
	})
	s = wait(func(s client.Workspace) bool {
		return s.Session.MemberSyncState == "ready" || s.Session.MemberSyncState == "limited"
	})
	if s.Session.MemberSyncState != "ready" {
		// The adapter publishes only a fixed explanation and numeric server code.
		t.Fatalf("member subscription limited: %s", s.Session.MemberSyncError)
	}
	peerNickname := os.Getenv("RESONA_TS_TEST_PEER_NICKNAME")
	s = wait(func(s client.Workspace) bool {
		selfFound, peerFound := false, peerNickname == ""
		for _, user := range s.Users {
			if user.ChannelID != target {
				continue
			}
			selfFound = selfFound || user.Self
			peerFound = peerFound || (!user.Self && user.Nickname == peerNickname)
		}
		return s.Session.ChannelID == target && selfFound && peerFound
	})
	if peerNickname != "" {
		for _, ch := range s.Channels {
			if ch.ID == target && ch.Members < 2 {
				t.Fatal("dedicated channel member count omitted self or test peer")
			}
		}
	}
	if _, err := service.DisconnectServer(); err != nil {
		t.Fatal(err)
	}
	s, err = service.GetWorkspace()
	if err != nil || s.Session.Mode != "offline" {
		t.Fatal("disconnect did not complete")
	}
	t.Log("dedicated channel move and member subscription observed through client service; disconnected")
}
