package client

import (
	"context"
	"errors"
	"testing"

	"github.com/tsukiyoz/resona/internal/audio"
)

func TestVoicePreferencesPrepareOnlyOneMutedConnectionStart(t *testing.T) {
	service, _ := serviceWithProfile(t, connectorFunc(func(context.Context, ServerProfile, string, func(RemoteState)) (RemoteConnection, error) {
		return &voiceConnection{}, nil
	}))
	defer service.Shutdown()
	engine := &fakeVoiceEngine{}
	created := 0
	service.newVoice = func(audio.Transport, func(audio.VoiceState)) voiceEngine { created++; return engine }
	config := audio.VoiceConfig{Enabled: true, Deafened: true, InputDeviceID: "chosen-input", OutputDeviceID: "chosen-output", Volume: 65, ActivationMode: "vad", VADThresholdDB: -35, NoiseSuppression: "medium", EchoCancellation: true, EchoSuppression: true, Ducking: true}
	state, err := service.SetVoicePreferences(config)
	if err != nil {
		t.Fatal(err)
	}
	config.Enabled, config.Muted, config.Deafened = false, true, false
	if state.VoiceConfig != config || state.Active || state.Busy || created != 0 {
		t.Fatalf("preferences activated devices or changed controls: %+v", state)
	}
	if _, err := service.ConnectServer("home", ""); err != nil {
		t.Fatal(err)
	}
	waitForMode(t, service, "connected")
	state = waitVoice(t, service, func(v VoiceState) bool { return v.Active && !v.Busy })
	config.Enabled = true
	if state.VoiceConfig != config || created != 1 {
		t.Fatalf("default startup lost preferences or opened twice: %+v created=%d", state, created)
	}
	if _, err := service.DisconnectServer(); err != nil {
		t.Fatal(err)
	}
	config.Enabled = false
	if got := service.GetVoiceState().VoiceConfig; got != config {
		t.Fatalf("disconnect lost preferences: %+v", got)
	}
}

func TestVoicePreferencesRejectUnsafeAdmissionAndInvalidConfig(t *testing.T) {
	for _, scenario := range []string{"connected", "connecting", "disconnecting", "busy", "shutdown", "invalid"} {
		t.Run(scenario, func(t *testing.T) {
			service, _ := serviceWithProfile(t, nil)
			original := service.GetVoiceState().VoiceConfig
			config := original
			config.Volume = 30
			service.mu.Lock()
			switch scenario {
			case "busy":
				service.voiceState.Busy = true
			case "shutdown":
				service.shutdown = true
			case "invalid":
				config.ActivationMode = "invalid"
			default:
				service.state.Session.Mode = scenario
			}
			service.mu.Unlock()
			if _, err := service.SetVoicePreferences(config); err == nil {
				t.Fatal("unsafe preference mutation accepted")
			}
			if got := service.GetVoiceState().VoiceConfig; got != original {
				t.Fatal("rejected preference mutation changed state")
			}
		})
	}
}

func TestFailedMicrophoneTestCanRetry(t *testing.T) {
	service, _ := serviceWithProfile(t, nil)
	failed := &fakeVoiceEngine{configure: func(context.Context, audio.VoiceConfig) error { return errors.New("device denied") }}
	service.newMicrophoneTest = func(func(audio.VoiceState)) voiceEngine { return failed }
	if _, err := service.StartMicrophoneTest(audio.VoiceConfig{Volume: 50}); err != nil {
		t.Fatal(err)
	}
	service.cleanupWG.Wait()
	if state := service.GetMicrophoneTest(); state.Error == "" || state.Busy || state.Active {
		t.Fatalf("failure state: %+v", state)
	}
	if failed.closed.Load() != 1 {
		t.Fatal("failed test retained resources")
	}
	service.newMicrophoneTest = func(func(audio.VoiceState)) voiceEngine { return &fakeVoiceEngine{} }
	if _, err := service.StartMicrophoneTest(audio.VoiceConfig{Volume: 50}); err != nil {
		t.Fatal("failed test blocked retry", err)
	}
	service.Shutdown()
}

func TestMicrophoneDeadlineDetachesUnresponsiveDeviceAndClosesLateResult(t *testing.T) {
	service, _ := serviceWithProfile(t, nil)
	started, release := make(chan struct{}), make(chan struct{})
	engine := &fakeVoiceEngine{configure: func(context.Context, audio.VoiceConfig) error {
		close(started)
		<-release
		return nil
	}}
	service.newMicrophoneTest = func(func(audio.VoiceState)) voiceEngine { return engine }
	if _, err := service.StartMicrophoneTest(audio.VoiceConfig{Volume: 50}); err != nil {
		t.Fatal(err)
	}
	<-started
	service.expireMicrophoneTest(engine)
	state := service.GetMicrophoneTest()
	if state.Busy || state.Enabled || state.Active || state.Error == "" {
		t.Fatalf("deadline left the device pending: %+v", state)
	}
	close(release)
	service.cleanupWG.Wait()
	if got := service.GetMicrophoneTest(); got.Busy || got.Active || got.Enabled || got.Error != state.Error || engine.closed.Load() != 1 {
		t.Fatalf("late device result changed state or escaped cleanup: %+v closes=%d", got, engine.closed.Load())
	}
	service.Shutdown()
}

func TestMicrophoneTestOfflineCancellationAndShutdown(t *testing.T) {
	started := make(chan struct{})
	engine := &fakeVoiceEngine{configure: func(ctx context.Context, config audio.VoiceConfig) error {
		if !config.Enabled || config.Muted || config.Deafened {
			t.Error("test did not explicitly enable local capture")
		}
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}
	service, _ := serviceWithProfile(t, nil)
	service.newMicrophoneTest = func(func(audio.VoiceState)) voiceEngine { return engine }
	if _, err := service.StartMicrophoneTest(audio.VoiceConfig{Volume: 75}); err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := service.StopMicrophoneTest(); err != nil {
		t.Fatal(err)
	}
	service.Shutdown()
	state := service.GetMicrophoneTest()
	if state.Active || state.Busy || state.Enabled || engine.closed.Load() != 1 {
		t.Fatalf("test survived shutdown: %+v close=%d", state, engine.closed.Load())
	}
}

func TestMicrophoneTestRequiresOfflineSession(t *testing.T) {
	service := voiceService(t, &fakeVoiceEngine{})
	if _, err := service.StartMicrophoneTest(audio.VoiceConfig{Volume: 100}); err == nil {
		t.Fatal("local test started during real connection")
	}
}

func TestMicrophoneTestReplacementClosesRetiredEngineFirst(t *testing.T) {
	service, _ := serviceWithProfile(t, nil)
	first, second := &fakeVoiceEngine{}, &fakeVoiceEngine{}
	service.newMicrophoneTest = func(func(audio.VoiceState)) voiceEngine { return first }
	_, err := service.StartMicrophoneTest(audio.VoiceConfig{Volume: 100})
	if err != nil {
		t.Fatal(err)
	}
	service.cleanupWG.Wait()
	service.microphoneTestMu.Lock()
	_, _ = service.StopMicrophoneTest()
	second.configure = func(context.Context, audio.VoiceConfig) error {
		if first.closed.Load() != 1 {
			t.Error("replacement opened before retired test closed")
		}
		return nil
	}
	service.newMicrophoneTest = func(func(audio.VoiceState)) voiceEngine { return second }
	if _, err := service.StartMicrophoneTest(audio.VoiceConfig{Volume: 100}); err != nil {
		t.Fatal(err)
	}
	service.microphoneTestMu.Unlock()
	service.cleanupWG.Wait()
	service.Shutdown()
	if first.closed.Load() != 1 || second.closed.Load() != 1 {
		t.Fatal("test ownership leaked")
	}
}
