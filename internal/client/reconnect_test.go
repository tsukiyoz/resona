package client

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/tsukiyoz/resona/internal/audio"
)

func TestHistoryArrivingDuringConnectionAdmissionSurvivesReconnect(t *testing.T) {
	var calls atomic.Int32
	updates := make(chan func(RemoteState), 2)
	s, _ := serviceWithProfile(t, connectorFunc(func(_ context.Context, _ ServerProfile, _ string, update func(RemoteState)) (RemoteConnection, error) {
		n := calls.Add(1)
		updates <- update
		state := RemoteState{ChannelID: "1", SelfID: "1", ChatState: "loading", Channels: []Channel{{ID: "1"}}}
		update(state)
		for seq := uint64(1); seq <= 3; seq++ {
			state.Messages = []RemoteMessage{{ChannelID: "1", UserID: "stable-author", Sequence: seq, Timestamp: time.Now().UnixNano(), Text: fmt.Sprintf("attempt-%d-message-%d", n, seq), History: true}}
			update(state)
		}
		state.Messages = nil
		state.ChatState = "ready"
		update(state)
		return &fakeConnection{}, nil
	}))
	defer s.Shutdown()
	s.reconnectDelay = time.Millisecond
	if _, err := s.ConnectServer("home", ""); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		state := waitForMode(t, s, "connected")
		if len(state.Messages) != 3 || state.Messages[0].Text != fmt.Sprintf("attempt-%d-message-1", attempt) || state.Session.ChatState != "ready" {
			t.Fatalf("admission lost history on attempt %d: %+v", attempt, state.Messages)
		}
		update := <-updates
		if attempt == 1 {
			update(RemoteState{Closed: true, Retryable: true})
		}
	}
}

func TestSetupMessageBufferIsBoundedAndChannelScoped(t *testing.T) {
	state := RemoteState{ChannelID: "1"}
	for i := range maxMessages + 20 {
		state = mergeSetupState(state, RemoteState{ChannelID: "1", Messages: []RemoteMessage{{Text: fmt.Sprint(i)}}})
	}
	if len(state.Messages) != maxMessages || state.Messages[0].Text != "20" {
		t.Fatal("setup history was not bounded")
	}
	state = mergeSetupState(state, RemoteState{ChannelID: "2"})
	if len(state.Messages) != 0 {
		t.Fatal("setup history leaked across channels")
	}
	state = mergeSetupState(state, RemoteState{Closed: true})
	if !mergeSetupState(state, RemoteState{ChannelID: "1"}).Closed {
		t.Fatal("setup closure was overwritten")
	}
}

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

func TestInitialConnectionRetriesAndRejectsLateAttemptCallbacks(t *testing.T) {
	for _, outcome := range []string{"success", "exhausted", "permanent", "closed_retry", "closed_permanent", "cancel_wait"} {
		t.Run(outcome, func(t *testing.T) {
			var calls atomic.Int32
			var old func(RemoteState)
			s, _ := serviceWithProfile(t, connectorFunc(func(_ context.Context, _ ServerProfile, _ string, update func(RemoteState)) (RemoteConnection, error) {
				n := calls.Add(1)
				if n == 1 {
					old = update
				}
				if outcome == "permanent" {
					return nil, &ConnectFailure{Message: "authentication refused"}
				}
				if outcome == "closed_retry" || outcome == "closed_permanent" {
					if n < 3 || outcome == "closed_permanent" {
						update(RemoteState{Closed: true, Retryable: outcome == "closed_retry", Error: "closed during setup"})
						return &fakeConnection{}, nil
					}
					update(RemoteState{ChannelID: "1"})
					return &fakeConnection{}, nil
				}
				if outcome == "success" && n == 3 {
					update(RemoteState{ChannelID: "1", Channels: []Channel{{ID: "1"}}})
					return &fakeConnection{}, nil
				}
				return nil, errors.New("temporary network failure")
			}))
			s.reconnectDelay = time.Millisecond
			if outcome == "cancel_wait" {
				s.reconnectDelay = time.Hour
			}
			defer s.Shutdown()
			_, _ = s.ConnectServer("home", "")
			if outcome == "cancel_wait" {
				waitConnectionState(t, s, func(state Session) bool { return state.ConnectionWaiting })
				_, _ = s.DisconnectServer()
				if calls.Load() != 1 || s.GetSessionState().Mode != "offline" {
					t.Fatal("cancelled wait dialed again")
				}
				return
			}
			wantMode, wantCalls := "failed", int32(3)
			if outcome == "success" || outcome == "closed_retry" {
				wantMode = "connected"
			}
			if outcome == "permanent" || outcome == "closed_permanent" {
				wantCalls = 1
			}
			state := waitForMode(t, s, wantMode)
			if calls.Load() != wantCalls {
				t.Fatalf("calls=%d", calls.Load())
			}
			old(RemoteState{Closed: true, Error: "stale"})
			if s.GetSessionState().Mode != wantMode {
				t.Fatal("old callback changed current state")
			}
			if wantMode == "failed" && (state.Session.ConnectionAttempt != int(wantCalls) || state.Session.ConnectionWaiting) {
				t.Fatal("failure attempt state incorrect")
			}
		})
	}
}

func waitConnectionState(t *testing.T, s *Service, predicate func(Session) bool) Session {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state := s.GetSessionState()
		if predicate(state) {
			return state
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("connection state timed out: %+v", s.GetSessionState())
	return Session{}
}

func TestImmediateRetryRacingTimerDoesNotWakeTheNextAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		now := make(chan struct{}, 1)
		woke := make(chan bool, 1)
		admit := make(chan struct{})
		done := make(chan struct{})
		go func() {
			woke <- waitConnectionRetry(ctx, time.Second, now)
			<-admit
			drainRetrySignal(now)
			close(done)
		}()
		synctest.Wait()
		synctest.Sleep(time.Second)
		if !<-woke {
			t.Fatal("retry timer did not wake the worker")
		}
		// The timer has already won; a click accepted before admission must
		// be consumed by the same attempt rather than wake the next one.
		now <- struct{}{}
		close(admit)
		<-done
		select {
		case <-now:
			t.Fatal("a click racing the timer survived admission")
		default:
		}
	})
}

func TestImmediateRetryIsRejectedDuringDial(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		var update func(RemoteState)
		finishDial := make(chan struct{})
		s, _ := serviceWithProfile(t, connectorFunc(func(ctx context.Context, _ ServerProfile, _ string, callback func(RemoteState)) (RemoteConnection, error) {
			if calls.Add(1) == 1 {
				update = callback
				callback(RemoteState{ChannelID: "1"})
				return &fakeConnection{}, nil
			}
			select {
			case <-finishDial:
			case <-ctx.Done():
			}
			return nil, errors.New("temporary failure")
		}))
		s.reconnectDelay = time.Second
		defer s.Shutdown()
		_, _ = s.ConnectServer("home", "")
		synctest.Wait()
		before := s.GetSessionState()
		if before.Mode != "connected" {
			t.Fatalf("initial connection failed: %+v", before)
		}
		update(RemoteState{Closed: true, Retryable: true})
		synctest.Wait()
		if _, err := s.RetryConnection(before.ID); err != nil {
			t.Fatal("retry while waiting was rejected", err)
		}
		synctest.Wait()
		if calls.Load() != 2 || s.GetSessionState().ConnectionWaiting {
			t.Fatal("immediate retry did not start one dial")
		}
		if _, err := s.RetryConnection(before.ID); err == nil {
			t.Fatal("retry during an in-flight dial was accepted")
		}
		close(finishDial)
		synctest.Wait()
		synctest.Sleep(time.Second - time.Nanosecond)
		if calls.Load() != 2 || !s.GetSessionState().ConnectionWaiting {
			t.Fatal("a retry signal bypassed the next backoff")
		}
		synctest.Sleep(time.Nanosecond)
		if calls.Load() != 3 {
			t.Fatal("the next scheduled retry did not run")
		}
	})
}

func TestReconnectThreeFailuresAreVisibleAndImmediateRetryWakesOneWorker(t *testing.T) {
	var calls atomic.Int32
	var old func(RemoteState)
	s, _ := serviceWithProfile(t, connectorFunc(func(_ context.Context, _ ServerProfile, _ string, update func(RemoteState)) (RemoteConnection, error) {
		n := calls.Add(1)
		if n == 1 {
			old = update
			update(RemoteState{ChannelID: "1"})
			return &fakeConnection{}, nil
		}
		return nil, errors.New("temporary network failure")
	}))
	s.reconnectDelay = time.Hour
	defer s.Shutdown()
	_, _ = s.ConnectServer("home", "")
	before := waitForMode(t, s, "connected")
	old(RemoteState{Closed: true, Retryable: true})
	for completed := 0; completed <= 3; completed++ {
		state := waitConnectionState(t, s, func(state Session) bool {
			return state.Mode == "reconnecting" && state.ConnectionWaiting && state.ConnectionAttempt == completed
		})
		if completed == 3 {
			if !state.ConnectionRetrySlow || state.Error == "" || calls.Load() != 4 {
				t.Fatal("three failed recovery attempts were not visible")
			}
			break
		}
		if _, err := s.RetryConnection(before.Session.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.RetryConnection("old-session"); err == nil {
		t.Fatal("stale retry accepted")
	}
	_, _ = s.DisconnectServer()
	if calls.Load() != 4 || s.GetSessionState().Mode != "offline" {
		t.Fatal("cancelled slow recovery dialed again")
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
