package desktopipc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
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

type watchedConnection struct {
	shutdownConnection
	watch func(context.Context, bool, bool) error
}

func (c *watchedConnection) SetResourceInterest(ctx context.Context, channels, members bool) error {
	return c.watch(ctx, channels, members)
}

type watchedConnector struct{ connection *watchedConnection }

func (c watchedConnector) Connect(_ context.Context, _ client.ServerProfile, _ string, update func(client.RemoteState)) (client.RemoteConnection, error) {
	update(client.RemoteState{ChannelID: "1", SelfID: "self", Channels: []client.Channel{{ID: "1", Name: "Lobby"}}})
	return c.connection, nil
}

func watchedService(t *testing.T, connection *watchedConnection) *client.Service {
	t.Helper()
	s, err := client.NewWithConnector(&memoryProfiles{profiles: []client.ServerProfile{{Protocol: "resona-noise", ServerPublicKey: "abababababababababababababababababababababababababababababababab", ID: "test", Name: "Test", Address: "example.invalid", Nickname: "Tester"}}}, watchedConnector{connection})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)
	_, err = s.ConnectServer("test", "")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for s.GetSessionState().Mode != "connected" {
		if time.Now().After(deadline) {
			t.Fatal("connect timed out")
		}
		time.Sleep(time.Millisecond)
	}
	return s
}

func waitResourceState(t *testing.T, events <-chan resourceSyncStatus, generation uint64, state string) resourceSyncStatus {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if event.Generation == generation && event.State == state {
				return event
			}
		case <-timer.C:
			t.Fatalf("resource state %s/%d timed out", state, generation)
		}
	}
}

func TestResourceStatusAlwaysFollowsConnectedWorkspace(t *testing.T) {
	s := watchedService(t, &watchedConnection{})
	for _, state := range []string{"syncing", "ready", "failed"} {
		t.Run(state, func(t *testing.T) {
			output := &resourceOutput{changed: make(chan struct{}, 1)}
			encoder := json.NewEncoder(output)
			published := ""
			status := resourceSyncStatus{SessionID: s.ResourceSessionID(), State: state}
			if err := publishResourceSync(encoder, s, status, true, &published); err != nil {
				t.Fatal(err)
			}
			if len(output.events) != 2 || output.events[0].Event != "workspace" || output.events[1].Event != "resourceSync" {
				t.Fatal("status arrived before its session")
			}
			var workspace client.Workspace
			_ = json.Unmarshal(output.events[0].Result, &workspace)
			if workspace.Session.Mode != "connected" || workspace.Session.ID != status.SessionID {
				t.Fatal("wrong session published before status")
			}
			if err := publishResourceSync(encoder, s, status, true, &published); err != nil {
				t.Fatal(err)
			}
			if len(output.events) != 3 || output.events[2].Event != "resourceSync" {
				t.Fatal("repeated status republished the workspace")
			}
		})
	}
}

func TestResourceRetriesStopAndRequireExplicitRetry(t *testing.T) {
	for _, permanent := range []bool{false, true} {
		t.Run(fmt.Sprint(permanent), func(t *testing.T) {
			var calls atomic.Int32
			var fail atomic.Bool
			connection := &watchedConnection{watch: func(context.Context, bool, bool) error {
				calls.Add(1)
				if fail.Load() {
					return &client.ResourceSyncError{Message: "resource unavailable", Retryable: !permanent}
				}
				return nil
			}}
			s := watchedService(t, connection)
			r := newResourceSync()
			r.retryDelay = time.Millisecond
			r.set(resourceInterest{ResourceInterest: client.ResourceInterest{Active: true}, Generation: 1})
			events := make(chan resourceSyncStatus, 32)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); r.run(ctx, s, func(status resourceSyncStatus) { events <- status }) }()
			defer func() { cancel(); <-done }()
			waitResourceState(t, events, 1, "ready")
			fail.Store(true)
			r.set(resourceInterest{ResourceInterest: client.ResourceInterest{Active: true, AllChannels: true}, Generation: 2})
			waitResourceState(t, events, 2, "refreshing")
			failed := waitResourceState(t, events, 2, "failed")
			want := int32(4)
			if permanent {
				want = 2
			}
			if calls.Load() != want || failed.Error == "" || s.GetSessionState().Mode != "connected" {
				t.Fatalf("bad retry/failure state: calls=%d status=%+v", calls.Load(), failed)
			}
			r.set(resourceInterest{ResourceInterest: client.ResourceInterest{Active: false}, Generation: 3})
			waitResourceState(t, events, 3, "failed")
			r.set(resourceInterest{ResourceInterest: client.ResourceInterest{Active: true}, Generation: 4})
			waitResourceState(t, events, 4, "failed")
			if calls.Load() != want {
				t.Fatal("focus changes retried failed fetch automatically")
			}
			fail.Store(false)
			r.set(resourceInterest{ResourceInterest: client.ResourceInterest{Active: true}, Generation: 5, Retry: true})
			waitResourceState(t, events, 5, "syncing")
			waitResourceState(t, events, 5, "ready")
			if calls.Load() != want+1 {
				t.Fatal("manual retry did not run once")
			}
		})
	}
}

func TestResourceScopeChangeAndDisconnectCancelTheOnlyRequest(t *testing.T) {
	var calls, active, maximum atomic.Int32
	started := make(chan bool, 4)
	var block atomic.Bool
	block.Store(true)
	connection := &watchedConnection{watch: func(ctx context.Context, channels, _ bool) error {
		calls.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for n > maximum.Load() {
			maximum.CompareAndSwap(maximum.Load(), n)
		}
		started <- channels
		if block.Load() {
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}}
	s := watchedService(t, connection)
	r := newResourceSync()
	r.set(resourceInterest{ResourceInterest: client.ResourceInterest{Active: true, AllChannels: true}, Generation: 1})
	events := make(chan resourceSyncStatus, 32)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); r.run(ctx, s, func(status resourceSyncStatus) { events <- status }) }()
	defer func() { cancel(); <-done }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request not started")
	}
	block.Store(false)
	r.set(resourceInterest{ResourceInterest: client.ResourceInterest{Active: false}, Generation: 2})
	waitResourceState(t, events, 2, "ready")
	if <-started || calls.Load() != 2 || maximum.Load() != 1 {
		t.Fatal("scope change did not cancel and serialize latest request")
	}
	block.Store(true)
	r.set(resourceInterest{ResourceInterest: client.ResourceInterest{Active: true}, Generation: 3})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request not restarted")
	}
	_, _ = s.DisconnectServer()
	waitResourceState(t, events, 3, "idle")
	if active.Load() != 0 || maximum.Load() != 1 {
		t.Fatal("disconnect left request running")
	}
	block.Store(false)
	_, _ = s.ConnectServer("test", "")
	ready := waitResourceState(t, events, 3, "ready")
	if ready.SessionID == "" || ready.SessionID != s.ResourceSessionID() {
		t.Fatal("new session state was not applied")
	}
}
