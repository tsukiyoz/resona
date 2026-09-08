//go:build integration

package ts3

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tsukiyoz/resona/internal/client"
)

// The caller's existing keeper sends one restricted SHA-256 reply from the
// temporary channel it created. This test opens only one additional connection.
// No server-wide or private messages are sent.
func TestLiveDedicatedChannelText(t *testing.T) {
	address, target, name, identity := os.Getenv("RESONA_TS_ADDRESS"), os.Getenv("RESONA_TS_TEST_CHANNEL_ID"), os.Getenv("RESONA_TS_TEST_CHANNEL_NAME"), os.Getenv("RESONA_TS_IDENTITY")
	if address == "" || target == "" || name == "" || identity == "" {
		t.Skip("authorized identity and dedicated test channel environment are required")
	}
	if !strings.HasPrefix(name, "Resona Test ") || !validID(target) {
		t.Fatal("dedicated Resona test channel required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	wait := func(service *client.Service, done func(client.Workspace) bool) client.Workspace {
		t.Helper()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			s, err := service.GetWorkspace()
			if err != nil {
				t.Fatal("workspace unavailable")
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
				t.Fatal("dedicated channel text verification timeout")
			}
		}
	}
	connect := func(nickname string) *client.Service {
		t.Helper()
		s, err := client.NewWithConnector(integrationProfiles{{ID: "integration", Name: "Integration", Address: address, Nickname: nickname}}, New(identity))
		if err != nil {
			t.Fatal("service initialization failed")
		}
		t.Cleanup(s.Shutdown)
		if _, err := s.ConnectServer("integration", os.Getenv("RESONA_TS_PASSWORD")); err != nil {
			t.Fatal("connect could not start")
		}
		state := wait(s, func(state client.Workspace) bool { return state.Session.Mode == "connected" })
		found := false
		for _, ch := range state.Channels {
			if ch.ID == target && ch.Name == name && ch.Kind != "separator" && !ch.PasswordRequired {
				found = true
			}
		}
		if !found {
			t.Fatal("dedicated channel ID/name absent or protected; refusing to move/send")
		}
		if _, err := s.SelectChannel(target); err != nil {
			t.Fatal("dedicated channel move rejected")
		}
		wait(s, func(state client.Workspace) bool {
			return state.Session.ChannelID == target && state.Session.SwitchingChannelID == ""
		})
		return s
	}
	a := connect("Resona-Verify")
	var keeperID string
	state := wait(a, func(s client.Workspace) bool {
		for _, member := range s.Users {
			if !member.Self && member.ChannelID == target && member.Nickname == "Resona-TestKeeper" {
				keeperID = member.ID
				return s.Session.ChannelID == target
			}
		}
		return false
	})
	text := "Resona isolated text verification: " + rand.Text() + " 中文 | <plain> \\ line\nsecond line return_code=abc " + strings.Repeat("fragment-check ", 60)
	if len(text) <= 487 || len(text) > client.MaxChannelMessageBytes {
		t.Fatal("verification payload must span UDP command fragments within the text limit")
	}
	digest := sha256.Sum256([]byte(text))
	const replyPrefix = "Resona isolated text reply sha256:"
	expectedReply := fmt.Sprintf("%s%x", replyPrefix, digest)
	if state.Session.ChannelID != target {
		t.Fatal("sender left dedicated channel; refusing to send")
	}
	pending, err := a.SendChannelMessage(state.Session.ID, target, text)
	if err != nil {
		t.Fatal("send could not start")
	}
	id := pending.Session.SendingMessageID
	wait(a, func(s client.Workspace) bool {
		for _, m := range s.Messages {
			if m.ID == id {
				if m.Status == "failed" || m.Status == "unconfirmed" {
					t.Fatalf("server did not confirm message: %s", m.Error)
				}
				return m.Status == "sent" && m.Text == text && m.ChannelID == target
			}
		}
		return false
	})
	state = wait(a, func(s client.Workspace) bool {
		for _, m := range s.Messages {
			if m.Status == "received" && m.ChannelID == target && m.AuthorID == keeperID && strings.HasPrefix(m.Text, replyPrefix) {
				if m.Text != expectedReply {
					t.Fatal("fixture digest does not match the complete original UTF-8 payload")
				}
				return true
			}
		}
		return false
	})
	if len(state.Messages) != 2 {
		t.Fatalf("unexpected duplicate or unrelated messages: %d", len(state.Messages))
	}
	spacers, custom, loaded := 0, 0, 0
	for _, ch := range state.Channels {
		if ch.Kind == "separator" {
			spacers++
		}
		if customIconID(ch.IconID) {
			custom++
			if ch.IconRef != "" {
				loaded++
			}
		}
	}
	if _, err := a.DisconnectServer(); err != nil {
		t.Fatal("disconnect failed")
	}
	state, _ = a.GetWorkspace()
	if state.Session.Mode != "offline" || len(state.Messages) != 2 {
		t.Fatal("disconnect lost message history")
	}
	t.Logf("dedicated keeper SHA-256 round-trip, UTF-8/escaping/fragments, self-echo suppression, and offline history verified; server presentation: spacers=%d custom_icons=%d loaded_icons=%d", spacers, custom, loaded)
}
