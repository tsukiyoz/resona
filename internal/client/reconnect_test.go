package client

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tsukiyoz/resona/internal/audio"
)

func TestReconnectDoesNotRepeatAutomaticUnmute(t *testing.T) {
	updates := make(chan func(RemoteState), 2)
	service, _ := serviceWithProfile(t, connectorFunc(func(_ context.Context, _ ServerProfile, _ string, update func(RemoteState)) (RemoteConnection, error) {
		updates <- update
		update(RemoteState{ChannelID: "1", SelfID: "1", Channels: []Channel{{ID: "1"}}})
		return &voiceConnection{}, nil
	}))
	defer service.Shutdown()
	service.reconnectDelay = time.Millisecond
	service.newVoice = func(audio.Transport, func(audio.VoiceState)) voiceEngine { return &fakeVoiceEngine{} }
	config := defaultVoiceConfig()
	config.AutoUnmuteOnConnect = true
	if _, err := service.SetVoicePreferences(config); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConnectServer("home", ""); err != nil {
		t.Fatal(err)
	}
	waitForMode(t, service, "connected")
	before := waitVoice(t, service, func(v VoiceState) bool { return v.Active && !v.Busy })
	if before.Muted {
		t.Fatal("first connection did not open microphone")
	}
	(<-updates)(RemoteState{Closed: true, Retryable: true})
	waitForMode(t, service, "connected")
	after := waitVoice(t, service, func(v VoiceState) bool { return v.Generation > before.Generation && v.Active && !v.Busy })
	if !after.Muted || !after.AutoUnmuteOnConnect {
		t.Fatalf("reconnect changed mute policy: %+v", after)
	}
}

func TestReconnectRetriesRestoresChannelAndCancelsOldCallbacks(t *testing.T) {
	var calls atomic.Int32
	updates := make(chan func(RemoteState), 4)
	connector := connectorFunc(func(ctx context.Context, p ServerProfile, password string, update func(RemoteState)) (RemoteConnection, error) {
		n := calls.Add(1)
		if password != "session-secret" {
			t.Error("session credential was not reused")
		}
		if n == 2 {
			return nil, errors.New("network unavailable")
		}
		updates <- update
		channel := "1"
		if n == 1 {
			channel = "2"
		}
		update(RemoteState{ChannelID: channel, SelfID: "1", Channels: []Channel{{ID: "1"}, {ID: "2"}}})
		return &fakeConnection{move: func(ctx context.Context, id string) error {
			if id != "2" {
				t.Error("wrong restore channel")
			}
			update(RemoteState{ChannelID: id, SelfID: "1", Channels: []Channel{{ID: "1"}, {ID: "2"}}})
			return nil
		}}, nil
	})
	s, _ := serviceWithProfile(t, connector)
	s.reconnectDelay = time.Millisecond
	defer s.Shutdown()
	_, _ = s.ConnectServer("home", "session-secret")
	before := waitForMode(t, s, "connected")
	old := <-updates
	s.mu.Lock()
	s.state.Messages = []Message{{ID: "old", ChannelID: "2", Text: "old channel text"}}
	s.mu.Unlock()
	old(RemoteState{Closed: true, Retryable: true})
	after := waitForMode(t, s, "connected")
	if calls.Load() != 3 || after.Session.ChannelID != "2" || after.Session.ID == before.Session.ID {
		t.Fatalf("bad recovery: calls=%d session=%+v", calls.Load(), after.Session)
	}
	if len(after.Messages) != 0 {
		t.Fatal("reconnected session retained old channel text")
	}
	old(RemoteState{Closed: true, Retryable: true})
	state, _ := s.GetWorkspace()
	if state.Session.Mode != "connected" {
		t.Fatal("late old callback closed new connection")
	}
	if !s.GetVoiceState().Muted {
		t.Fatal("recovery must not resume microphone")
	}
}

func TestReconnectAuthenticationStopsAndCancelAbortsDial(t *testing.T) {
	for _, mode := range []string{"authentication", "cancel", "shutdown", "backoff"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			updates := make(chan func(RemoteState), 1)
			dialing := make(chan struct{})
			s, _ := serviceWithProfile(t, connectorFunc(func(ctx context.Context, p ServerProfile, password string, update func(RemoteState)) (RemoteConnection, error) {
				if calls.Add(1) == 1 {
					updates <- update
					update(RemoteState{ChannelID: "1"})
					return &fakeConnection{}, nil
				}
				close(dialing)
				if mode == "authentication" {
					return nil, &ConnectFailure{Message: "authentication refused"}
				}
				<-ctx.Done()
				return nil, ctx.Err()
			}))
			s.reconnectDelay = time.Millisecond
			if mode == "backoff" {
				s.reconnectDelay = time.Hour
			}
			defer s.Shutdown()
			_, _ = s.ConnectServer("home", "")
			waitForMode(t, s, "connected")
			(<-updates)(RemoteState{Closed: true, Retryable: true})
			if mode == "authentication" {
				waitForMode(t, s, "failed")
				if calls.Load() != 2 {
					t.Fatal("authentication retried")
				}
				return
			}
			if mode != "backoff" {
				select {
				case <-dialing:
				case <-time.After(time.Second):
					t.Fatal("no retry dial")
				}
			}
			done := make(chan struct{})
			go func() {
				if mode == "shutdown" {
					s.Shutdown()
				} else {
					_, _ = s.DisconnectServer()
				}
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("cancel blocked")
			}
			state, _ := s.GetWorkspace()
			if state.Session.Mode != "offline" {
				t.Fatalf("cancel left mode %s", state.Session.Mode)
			}
			if mode == "backoff" && calls.Load() != 1 {
				t.Fatal("cancelled backoff dialed")
			}
		})
	}
}

func TestReconnectBackoffBounded(t *testing.T) {
	for attempt := range 100 {
		d := reconnectBackoff(attempt)
		if d < time.Second || d > 30*time.Second {
			t.Fatalf("delay=%s", d)
		}
	}
}

func TestReconnectInterruptedRestorationAndMissingChannel(t *testing.T) {
	for _, mode := range []string{"retry", "missing", "cancel", "refused"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			updates := make(chan func(RemoteState), 1)
			moving := make(chan struct{}, 1)
			s, _ := serviceWithProfile(t, connectorFunc(func(ctx context.Context, p ServerProfile, password string, update func(RemoteState)) (RemoteConnection, error) {
				n := calls.Add(1)
				channel := "1"
				if n == 1 {
					channel = "2"
					updates <- update
				}
				update(RemoteState{ChannelID: channel})
				return &fakeConnection{move: func(ctx context.Context, id string) error {
					if mode == "missing" {
						return ErrChannelPermissionDenied
					}
					if mode == "cancel" {
						moving <- struct{}{}
						<-ctx.Done()
						return ctx.Err()
					}
					if n == 2 {
						update(RemoteState{Closed: true, Retryable: mode != "refused"})
						return errors.New("connection dropped")
					}
					update(RemoteState{ChannelID: id})
					return nil
				}}, nil
			}))
			s.reconnectDelay = time.Millisecond
			defer s.Shutdown()
			_, _ = s.ConnectServer("home", "")
			waitForMode(t, s, "connected")
			(<-updates)(RemoteState{Closed: true, Retryable: true})
			if mode == "cancel" {
				select {
				case <-moving:
				case <-time.After(time.Second):
					t.Fatal("not restoring")
				}
				done := make(chan struct{})
				go func() { s.Shutdown(); close(done) }()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("restoration cancellation hung")
				}
				return
			}
			if mode == "refused" {
				waitForMode(t, s, "failed")
				s.mu.Lock()
				retained := s.reconnect != nil
				s.mu.Unlock()
				if retained || calls.Load() != 2 {
					t.Fatal("terminal restoration retained credentials or retried")
				}
				return
			}
			state := waitForMode(t, s, "connected")
			want := "2"
			if mode == "missing" {
				want = "1"
			}
			if state.Session.ChannelID != want || !s.GetVoiceState().Muted {
				t.Fatalf("unsafe restore: %+v", state.Session)
			}
			if mode == "retry" && calls.Load() != 3 {
				t.Fatal("interrupted restore did not retry")
			}
		})
	}
}
