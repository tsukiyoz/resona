package desktopipc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/tsukiyoz/resona/internal/client"
)

type resourceOutput struct {
	mu     sync.Mutex
	events []struct {
		ID     uint64
		Event  string
		Result json.RawMessage
	}
	changed chan struct{}
}

func (r *resourceOutput) Write(data []byte) (int, error) {
	var event struct {
		ID     uint64
		Event  string
		Result json.RawMessage
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return 0, err
	}
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
	select {
	case r.changed <- struct{}{}:
	default:
	}
	return len(data), nil
}
func (r *resourceOutput) Close() error { return nil }
func (r *resourceOutput) wait(t *testing.T, id uint64, event string, generation uint64) json.RawMessage {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		r.mu.Lock()
		for _, e := range r.events {
			var meta struct{ Generation uint64 }
			_ = json.Unmarshal(e.Result, &meta)
			if (id != 0 && e.ID == id) || (event != "" && e.Event == event && meta.Generation == generation) {
				r.mu.Unlock()
				return e.Result
			}
		}
		r.mu.Unlock()
		select {
		case <-r.changed:
		case <-deadline.C:
			t.Fatal("IPC event timed out")
		}
	}
}

func TestIPCBackgroundCoalescesResourcesAndResumesOnce(t *testing.T) {
	s, err := client.New(&memoryProfiles{})
	if err != nil {
		t.Fatal(err)
	}
	parent, child := net.Pipe()
	defer parent.Close()
	output := &resourceOutput{changed: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, s, child, output) }()
	output.wait(t, 0, "workspace", 0)
	encoder := json.NewEncoder(parent)
	call := func(id uint64, active bool) {
		t.Helper()
		if err := encoder.Encode(map[string]any{"id": id, "method": "SetResourceInterest", "params": map[string]any{"active": active, "allChannels": active, "allMembers": active, "generation": id}}); err != nil {
			t.Fatal(err)
		}
		output.wait(t, id, "", 0)
	}
	call(1, false)
	output.mu.Lock()
	start := len(output.events)
	output.mu.Unlock()
	for i := range 30 {
		if _, err := s.SaveServer(client.ServerProfile{Protocol: "resona-noise", ServerPublicKey: "abababababababababababababababababababababababababababababababab", Name: fmt.Sprintf("server-%d", i), Address: "example.invalid", Nickname: "tester"}); err != nil {
			t.Fatal(err)
		}
	}
	// Permit all background notifications to drain before restoring the window.
	time.Sleep(60 * time.Millisecond)
	output.mu.Lock()
	for _, e := range output.events[start:] {
		if e.Event == "workspace" {
			t.Error("background metadata reached UI")
		}
	}
	output.mu.Unlock()
	call(2, true)
	result := output.wait(t, 0, "resourceSync", 2)
	var restored struct{ Workspace client.Workspace }
	if json.Unmarshal(result, &restored) != nil || len(restored.Workspace.Servers) != 30 {
		t.Fatal("resume lost coalesced state")
	}
	t.Log("30 background resource mutations: 0 workspace events; one resume snapshot contains all 30")
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("resource worker leaked on exit")
	}
}

func TestBackgroundResourcePolicyKeepsCriticalControl(t *testing.T) {
	previous := client.Workspace{Session: client.Session{ID: "session", Mode: "connected"}}
	next := previous
	next.Channels = []client.Channel{{Name: "changed"}}
	next.Messages = []client.Message{{Text: "saved in core"}}
	next.Session.MemberSyncState = "limited"
	if publishWorkspace(false, &previous, next) {
		t.Fatal("background resource notification was not coalesced")
	}
	if !publishWorkspace(true, &previous, next) {
		t.Fatal("foreground changes must publish immediately")
	}
	next.Session.ServerRole = "member"
	if !publishWorkspace(false, &previous, next) {
		t.Fatal("permission change suppressed")
	}
	next = previous
	next.Session.Mode = "failed"
	if !publishWorkspace(false, &previous, next) {
		t.Fatal("disconnect suppressed")
	}
	voice := client.VoiceState{InputLevelDB: -20, LocalSpeaking: true, SpeakingClientIDs: []string{"2"}, Error: "device failed"}
	voice.Muted = true
	filtered := backgroundVoice(voice)
	if filtered.LocalSpeaking || filtered.SpeakingClientIDs != nil || filtered.InputLevelDB != 0 || !filtered.Muted || filtered.Error != voice.Error {
		t.Fatal("visual filtering affected control")
	}
}

func TestResourceMailboxIsBoundedAndKeepsLatest(t *testing.T) {
	r := newResourceSync()
	for i := uint64(1); i <= 1000; i++ {
		r.set(resourceInterest{Generation: i})
	}
	if len(r.changed) != 1 || r.current().Generation != 1000 {
		t.Fatal("unbounded or stale interest mailbox")
	}
}
