package client

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tsukiyoz/resona/internal/audio"
)

type voiceConnection struct{ fakeConnection }

func (*voiceConnection) SetVoiceHandler(func(audio.Packet))              {}
func (*voiceConnection) SendVoice([]byte, audio.Codec) error             { return nil }
func (*voiceConnection) VoiceCodec() (audio.Codec, error)                { return audio.CodecOpusVoice, nil }
func (*voiceConnection) SetVoiceMuted(context.Context, bool, bool) error { return nil }

type fakeVoiceEngine struct {
	mu        sync.Mutex
	state     audio.VoiceState
	configure func(context.Context, audio.VoiceConfig) error
	closed    atomic.Int32
	changed   atomic.Int32
}

func (e *fakeVoiceEngine) Configure(ctx context.Context, c audio.VoiceConfig) error {
	if e.configure != nil {
		if err := e.configure(ctx, c); err != nil {
			return err
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state = audio.VoiceState{Config: c, Active: c.Enabled && !c.Deafened, ChannelCodec: audio.CodecOpusVoice}
	return nil
}
func (e *fakeVoiceEngine) ChannelChanged(context.Context) error { e.changed.Add(1); return nil }
func (e *fakeVoiceEngine) Status() audio.VoiceState             { e.mu.Lock(); defer e.mu.Unlock(); return e.state }
func (e *fakeVoiceEngine) Close() error                         { e.closed.Add(1); return nil }

func voiceService(t *testing.T, e *fakeVoiceEngine) *Service {
	t.Helper()
	c := &voiceConnection{}
	s, _ := serviceWithProfile(t, connectorFunc(func(_ context.Context, _ ServerProfile, _ string, update func(RemoteState)) (RemoteConnection, error) {
		update(RemoteState{ChannelID: "10", SelfID: "1", Channels: []Channel{{ID: "10", Name: "Voice"}}})
		return c, nil
	}))
	s.newVoice = func(audio.Transport, func(audio.VoiceState)) voiceEngine { return e }
	if _, err := s.ConnectServer("home", ""); err != nil {
		t.Fatal(err)
	}
	waitForMode(t, s, "connected")
	t.Cleanup(s.Shutdown)
	return s
}

func waitVoice(t *testing.T, s *Service, check func(VoiceState) bool) VoiceState {
	t.Helper()
	deadline := time.After(2 * time.Second)
	changes, unsubscribe := s.SubscribeChanges()
	defer unsubscribe()
	for {
		state := s.GetVoiceState()
		if check(state) {
			return state
		}
		select {
		case <-changes:
		case <-deadline:
			t.Fatalf("voice state did not settle: %+v", state)
		}
	}
}

func TestSpeakingStateFiltersMembersAndDoesNotAliasSnapshots(t *testing.T) {
	s := voiceService(t, &fakeVoiceEngine{})
	s.mu.Lock()
	s.state.Users = []User{
		{ID: "1", Self: true, ChannelID: "10"},
		{ID: "2", ChannelID: "10"},
		{ID: "3", ChannelID: "20"},
	}
	s.voiceState = voiceSnapshot(audio.VoiceState{
		Config: audio.VoiceConfig{Enabled: true}, Active: true,
		SpeakingClientIDs: []uint16{1, 2, 3, 4}, LocalSpeaking: true,
	})
	s.mu.Unlock()
	state := s.GetVoiceState()
	if !state.LocalSpeaking || len(state.SpeakingClientIDs) != 1 || state.SpeakingClientIDs[0] != "2" {
		t.Fatalf("invalid activity membership: %+v", state)
	}
	state.SpeakingClientIDs[0] = "corrupted"
	if got := s.GetVoiceState(); got.SpeakingClientIDs[0] != "2" {
		t.Fatal("caller changed internal speaking state")
	}
	s.mu.Lock()
	s.voiceState.Muted = true
	s.mu.Unlock()
	if got := s.GetVoiceState(); got.LocalSpeaking || len(got.SpeakingClientIDs) != 1 {
		t.Fatalf("mute should clear only local activity: %+v", got)
	}
	s.mu.Lock()
	s.state.Users = s.state.Users[:1]
	s.mu.Unlock()
	if got := s.GetVoiceState(); len(got.SpeakingClientIDs) != 0 {
		t.Fatal("departed member still speaking")
	}
}

func TestSpeakingStateClearsForUnavailableVoiceAndChannelTransitions(t *testing.T) {
	for _, scenario := range []string{"offline", "switching", "busy", "disabled", "inactive", "deafened"} {
		t.Run(scenario, func(t *testing.T) {
			s := voiceService(t, &fakeVoiceEngine{})
			s.mu.Lock()
			s.state.Users = []User{{ID: "2", ChannelID: "10"}}
			s.voiceState = VoiceState{VoiceConfig: audio.VoiceConfig{Enabled: true}, Active: true, LocalSpeaking: true, SpeakingClientIDs: []string{"2"}}
			switch scenario {
			case "offline":
				s.state.Session.Mode = "offline"
			case "switching":
				s.state.Session.SwitchingChannelID = "20"
			case "busy":
				s.voiceState.Busy = true
			case "disabled":
				s.voiceState.Enabled = false
			case "inactive":
				s.voiceState.Active = false
			case "deafened":
				s.voiceState.Deafened = true
			}
			s.mu.Unlock()
			if got := s.GetVoiceState(); got.LocalSpeaking || len(got.SpeakingClientIDs) != 0 {
				t.Fatalf("stale activity in %s: %+v", scenario, got)
			}
		})
	}
}

func TestVoiceRequiresExplicitMutedStartAndDoesNotAlterTextSession(t *testing.T) {
	e := &fakeVoiceEngine{}
	s := voiceService(t, e)
	if state := s.GetVoiceState(); state.Enabled || !state.Muted {
		t.Fatalf("unsafe default: %+v", state)
	}
	if _, err := s.ConfigureVoice(audio.VoiceConfig{Enabled: true, Volume: 100}); err == nil {
		t.Fatal("automatic unmuted start accepted")
	}
	if _, err := s.ConfigureVoice(audio.VoiceConfig{Enabled: true, Muted: true, Volume: 70}); err != nil {
		t.Fatal(err)
	}
	waitVoice(t, s, func(v VoiceState) bool { return v.Active && !v.Busy })
	if _, err := s.ConfigureVoice(audio.VoiceConfig{Enabled: true, Muted: false, Deafened: true, Volume: 70}); err != nil {
		t.Fatal(err)
	}
	v := waitVoice(t, s, func(v VoiceState) bool { return !v.Busy && v.Deafened })
	if v.Muted || v.Active {
		t.Fatalf("deafen did not preserve mute preference: %+v", v)
	}
	if _, err := s.ConfigureVoice(audio.VoiceConfig{}); err != nil {
		t.Fatal(err)
	}
	s.cleanupWG.Wait()
	if e.closed.Load() != 1 {
		t.Fatal("disable did not close engine")
	}
	w, _ := s.GetWorkspace()
	if w.Session.Mode != "connected" {
		t.Fatal("disable disconnected text session")
	}
}

func TestDisconnectCancelsVoiceStartupAndIgnoresLateCompletion(t *testing.T) {
	started := make(chan struct{})
	e := &fakeVoiceEngine{configure: func(ctx context.Context, _ audio.VoiceConfig) error { close(started); <-ctx.Done(); return ctx.Err() }}
	s := voiceService(t, e)
	if _, err := s.ConfigureVoice(audio.VoiceConfig{Enabled: true, Muted: true, Volume: 100}); err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := s.DisconnectServer(); err != nil {
		t.Fatal(err)
	}
	v := s.GetVoiceState()
	if v.Enabled || v.Active || v.Busy || !v.Muted || e.closed.Load() != 1 {
		t.Fatalf("late startup survived disconnect: %+v", v)
	}
}

func TestVoiceChangedChannelReconfiguresOutsideCallback(t *testing.T) {
	e := &fakeVoiceEngine{}
	s := voiceService(t, e)
	if _, err := s.ConfigureVoice(audio.VoiceConfig{Enabled: true, Muted: true, Volume: 100}); err != nil {
		t.Fatal(err)
	}
	waitVoice(t, s, func(v VoiceState) bool { return v.Active && !v.Busy })
	s.applyRemoteState(s.generation, RemoteState{ChannelID: "11", SelfID: "1", Channels: []Channel{{ID: "11", Name: "Other"}}})
	waitVoice(t, s, func(v VoiceState) bool { return !v.Busy && e.changed.Load() == 1 })
}

func TestCancelledVoiceOperationWaitingForDevicesClearsBusy(t *testing.T) {
	for _, channelChange := range []bool{false, true} {
		t.Run(map[bool]string{false: "configure", true: "channel change"}[channelChange], func(t *testing.T) {
			e := &fakeVoiceEngine{}
			s := voiceService(t, e)
			if channelChange {
				_, _ = s.ConfigureVoice(audio.VoiceConfig{Enabled: true, Muted: true, Volume: 100})
				waitVoice(t, s, func(v VoiceState) bool { return v.Active && !v.Busy })
			}
			s.voiceMu.Lock()
			if channelChange {
				s.mu.Lock()
				s.voiceChannelChangedLocked()
				s.mu.Unlock()
			} else {
				_, _ = s.ConfigureVoice(audio.VoiceConfig{Enabled: true, Muted: true, Volume: 100})
			}
			s.mu.Lock()
			s.voiceCancel()
			s.mu.Unlock()
			s.voiceMu.Unlock()
			waitVoice(t, s, func(v VoiceState) bool { return !v.Busy && v.Error != "" })
			w, _ := s.GetWorkspace()
			if w.Session.Mode != "connected" {
				t.Fatal("device wait cancellation disconnected text")
			}
		})
	}
}

func TestLateVoiceNotificationCannotRestorePreviousMuteState(t *testing.T) {
	e := &fakeVoiceEngine{}
	s := voiceService(t, e)
	config := audio.VoiceConfig{Enabled: true, Muted: true, Volume: 100}
	_, _ = s.ConfigureVoice(config)
	old := waitVoice(t, s, func(v VoiceState) bool { return v.Active && !v.Busy })
	config.Muted = false
	_, _ = s.ConfigureVoice(config)
	waitVoice(t, s, func(v VoiceState) bool { return !v.Busy && !v.Muted })
	s.applyVoiceState(s.voiceEpoch, s.generation, audio.VoiceState{Config: old.VoiceConfig, Active: true})
	if s.GetVoiceState().Muted {
		t.Fatal("queued old notification restored mute")
	}
}
