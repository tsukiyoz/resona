package client

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

type memoryStore struct {
	profiles []ServerProfile
	err      error
}

func (s *memoryStore) Load() ([]ServerProfile, error) { return s.profiles, s.err }
func (s *memoryStore) Save(profiles []ServerProfile) error {
	if s.err != nil {
		return s.err
	}
	s.profiles = append([]ServerProfile{}, profiles...)
	return nil
}

func TestPreviewSessionBoundaries(t *testing.T) {
	service, err := New(&memoryStore{})
	if err != nil {
		t.Fatal(err)
	}
	state, _ := service.GetWorkspace()
	if state.Session.Mode != "offline" || state.Channels == nil || state.Messages == nil {
		t.Fatalf("unexpected initial state: %+v", state)
	}
	if _, err := service.SendMessage("hello"); err == nil {
		t.Fatal("offline messages must fail")
	}
	if _, err := service.SelectChannel("lobby"); err == nil {
		t.Fatal("offline selection must fail")
	}
	state, _ = service.OpenPreview()
	if state.Session.Mode != "preview" || len(state.Channels) != 3 {
		t.Fatalf("unexpected preview: %+v", state)
	}
	if _, err := service.SelectChannel("unknown"); err == nil {
		t.Fatal("unknown channel must fail")
	}
	state, _ = service.SelectChannel("music")
	members := 0
	for _, c := range state.Channels {
		members += c.Members
	}
	if members != 1 || state.Session.ChannelID != "music" {
		t.Fatalf("invalid member state: %+v", state)
	}
	state, err = service.SendMessage("  hello  ")
	if err != nil || len(state.Messages) != 1 || state.Messages[0].Text != "hello" || state.Messages[0].ChannelID != "music" {
		t.Fatalf("unexpected message: %+v, %v", state, err)
	}
	if _, err := service.SendMessage(strings.Repeat("x", 2001)); err == nil {
		t.Fatal("oversized messages must fail")
	}
	state.Messages[0].Text = "mutated snapshot"
	state, _ = service.GetWorkspace()
	if state.Messages[0].Text != "hello" {
		t.Fatal("snapshot aliases internal state")
	}
	state, _ = service.LeavePreview()
	if state.Session.Mode != "offline" || len(state.Messages) != 0 || len(state.Channels) != 0 {
		t.Fatal("preview state survived leaving")
	}
	state, _ = service.OpenPreview()
	if len(state.Messages) != 0 {
		t.Fatal("preview messages survived reopening")
	}
}

func TestFailedPersistenceDoesNotChangeWorkspace(t *testing.T) {
	store := &memoryStore{}
	service, _ := New(store)
	profile := ServerProfile{Name: "Home", Address: "localhost:9987", Nickname: "Alice"}
	state, err := service.SaveServer(profile)
	if err != nil || len(state.Servers) != 1 || state.Servers[0].ID == "" {
		t.Fatalf("save failed: %+v %v", state, err)
	}
	profile = state.Servers[0]
	profile.Name = "Changed"
	store.err = errors.New("disk unavailable")
	if _, err := service.SaveServer(profile); err == nil {
		t.Fatal("failed save must be reported")
	}
	if _, err := service.DeleteServer(profile.ID); err == nil {
		t.Fatal("failed delete must be reported")
	}
	state, _ = service.GetWorkspace()
	if len(state.Servers) != 1 || state.Servers[0].Name != "Home" {
		t.Fatalf("failed persistence changed state: %+v", state)
	}
	store.err = nil
	state, err = service.DeleteServer(profile.ID)
	if err != nil || len(state.Servers) != 0 {
		t.Fatalf("delete failed: %+v %v", state, err)
	}
}

func TestProfileValidation(t *testing.T) {
	for _, address := range []string{"localhost", "voice.example.org:9987", "127.0.0.1", "::1", "[::1]:9987"} {
		if err := ValidateProfile(ServerProfile{Name: "Home", Address: address, Nickname: "Alice"}); err != nil {
			t.Errorf("valid address %q: %v", address, err)
		}
	}
	for _, address := range []string{"", "https://example.org", "host:0", "host:65536", "host:abc", "bad host", "-bad.example", "example..org", "[::1]"} {
		if err := ValidateProfile(ServerProfile{Name: "Home", Address: address, Nickname: "Alice"}); err == nil {
			t.Errorf("accepted invalid address %q", address)
		}
	}
	if err := ValidateProfile(ServerProfile{Name: "Home", Address: "localhost", Nickname: "\nAlice"}); err == nil {
		t.Fatal("nickname control characters accepted")
	}
}

func TestConcurrentBookmarkRequestsAreSerialized(t *testing.T) {
	service, err := New(&memoryStore{})
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 20 {
		group.Go(func() {
			_, err := service.SaveServer(ServerProfile{Name: "Home", Address: "localhost", Nickname: "Alice"})
			if err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	state, err := service.GetWorkspace()
	if err != nil || len(state.Servers) != 20 {
		t.Fatalf("lost concurrent bookmark update: %d servers, %v", len(state.Servers), err)
	}
}
