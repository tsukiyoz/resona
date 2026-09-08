package desktopipc

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/tsukiyoz/resona/internal/client"
)

type memoryProfiles struct{ profiles []client.ServerProfile }

func (s *memoryProfiles) Load() ([]client.ServerProfile, error) { return s.profiles, nil }
func (s *memoryProfiles) Save(p []client.ServerProfile) error   { s.profiles = p; return nil }

func TestPipeCommandsEventsAndShutdown(t *testing.T) {
	s, err := client.New(&memoryProfiles{})
	if err != nil {
		t.Fatal(err)
	}
	parent, child := net.Pipe()
	defer parent.Close()
	_ = parent.SetDeadline(time.Now().Add(5 * time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, s, child, child) }()
	messages := make(chan map[string]json.RawMessage, 32)
	go func() {
		d := json.NewDecoder(parent)
		for {
			var m map[string]json.RawMessage
			if d.Decode(&m) != nil {
				return
			}
			messages <- m
		}
	}()
	encoder := json.NewEncoder(parent)
	call := func(id uint64, method string, params any) map[string]json.RawMessage {
		t.Helper()
		if err := encoder.Encode(map[string]any{"id": id, "method": method, "params": params}); err != nil {
			t.Fatal(err)
		}
		for {
			select {
			case message := <-messages:
				var got uint64
				_ = json.Unmarshal(message["id"], &got)
				if got == id {
					return message
				}
			case <-time.After(3 * time.Second):
				t.Fatal("missing response")
			}
		}
	}
	r := call(1, "SaveServer", map[string]any{"profile": client.ServerProfile{Name: "Club", Address: "example.invalid", Nickname: "Player"}})
	var w client.Workspace
	if err := json.Unmarshal(r["result"], &w); err != nil || len(w.Servers) != 1 {
		t.Fatalf("invalid saved snapshot: %s", r["result"])
	}
	r = call(2, "OpenPreview", nil)
	if len(r["error"]) > 0 {
		t.Fatalf("preview failed: %s", r["error"])
	}
	r = call(3, "SendMessage", map[string]string{"text": "hello"})
	if err := json.Unmarshal(r["result"], &w); err != nil || len(w.Messages) != 1 {
		t.Fatal("message not delivered to preview")
	}
	r = call(4, "ConfigureVoice", map[string]any{"enabled": true, "muted": true, "volume": 100})
	if len(r["error"]) == 0 {
		t.Fatal("preview opened audio transport")
	}
	call(5, "Shutdown", nil)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("sidecar did not shut down")
	}
	w, _ = s.GetWorkspace()
	if w.Session.Mode != "offline" {
		t.Fatal("shutdown left preview active")
	}
}

func TestParentEOFReleasesService(t *testing.T) {
	s, _ := client.New(&memoryProfiles{})
	_, _ = s.OpenPreview()
	parent, child := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), s, child, child) }()
	_ = parent.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("EOF failed to stop sidecar")
	}
	w, _ := s.GetWorkspace()
	if w.Session.Mode != "offline" {
		t.Fatal("EOF failed to clean up service")
	}
}
