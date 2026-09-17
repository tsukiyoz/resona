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
	config := audio.VoiceConfig{Enabled: true, Deafened: true, InputDeviceID: "chosen-input", OutputDeviceID: "chosen-output", Volume: 65, ActivationMode: "vad", VADThresholdDB: -35, Ducking: true}
	config.Processing.Preprocess[0] = audio.ProcessorSpec{Name: "aec", Backend: "speex", Params: audio.ProcessorParams{Residual: true}}
	config.Processing.Preprocess[1] = audio.ProcessorSpec{Name: "ans", Backend: "speex", Params: audio.ProcessorParams{Level: 2}}
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

func TestVoiceOperationDistinguishesAcceptanceApplicationAndFailure(t *testing.T) {
	engine := &fakeVoiceEngine{}
	service := voiceService(t, engine)
	before := service.GetVoiceState()
	started, release := make(chan struct{}), make(chan struct{})
	engine.configure = func(_ context.Context, config audio.VoiceConfig) error {
		close(started)
		<-release
		return nil
	}
	config := before.VoiceConfig
	config.Processing.Preprocess[1] = audio.ProcessorSpec{Name: "ans", Backend: "speex", Params: audio.ProcessorParams{Level: 2}}
	pending, err := service.ConfigureVoice(config)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if !pending.Busy || pending.Operation <= before.Operation || pending.AppliedOperation != before.AppliedOperation || pending.Generation != before.Generation {
		t.Fatalf("acceptance falsely confirmed application: before=%+v pending=%+v", before, pending)
	}
	close(release)
	applied := waitVoice(t, service, func(v VoiceState) bool { return !v.Busy && v.Operation == pending.Operation })
	if applied.AppliedOperation != pending.Operation || applied.Processing != config.Processing {
		t.Fatalf("application not confirmed: %+v", applied)
	}
	engine.configure = func(context.Context, audio.VoiceConfig) error { return errors.New("processor unavailable") }
	failedConfig := config
	failedConfig.Processing.Preprocess[1].Params.Level = 3
	rejected, err := service.ConfigureVoice(failedConfig)
	if err != nil {
		t.Fatal(err)
	}
	failed := waitVoice(t, service, func(v VoiceState) bool { return !v.Busy && v.Operation == rejected.Operation })
	if failed.AppliedOperation == rejected.Operation || failed.Processing != config.Processing || failed.Error == "" {
		t.Fatalf("failed replacement changed applied state: %+v", failed)
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

func TestOnlineMicrophoneTestRestoresVoiceIntent(t *testing.T) {
	for _, config := range []audio.VoiceConfig{
		{Enabled: true, Muted: true, Volume: 80, ActivationMode: "ptt"},
		{Enabled: true, Muted: false, Volume: 70, ActivationMode: "continuous"},
		{Enabled: true, Muted: false, Deafened: true, Volume: 60, ActivationMode: "vad"},
		{Enabled: false, Muted: true, Volume: 50},
	} {
		t.Run(config.ActivationMode, func(t *testing.T) {
			engine := &fakeVoiceEngine{}
			service := voiceService(t, engine)
			if _, err := service.ConfigureVoice(config); err != nil {
				t.Fatal(err)
			}
			waitVoice(t, service, func(v VoiceState) bool { return !v.Busy })
			before := service.GetVoiceState().VoiceConfig
			if _, err := service.StartMicrophoneTest(audio.VoiceConfig{Volume: 100, ActivationMode: "ptt"}); err != nil {
				t.Fatal(err)
			}
			waitVoice(t, service, func(v VoiceState) bool { return !v.Busy && v.LocalMonitor })
			if !service.GetMicrophoneTest().Active {
				t.Fatal("online test not active")
			}
			if service.GetVoiceState().Volume != before.Volume {
				t.Fatal("test changed channel playback volume")
			}
			if _, err := service.StartMicrophoneTest(audio.VoiceConfig{Volume: 100}); err == nil {
				t.Fatal("duplicate test accepted")
			}
			if _, err := service.ConfigureVoice(audio.VoiceConfig{Enabled: true, Volume: 80}); err == nil {
				t.Fatal("normal control raced test")
			}
			if _, err := service.StopMicrophoneTest(); err != nil {
				t.Fatal(err)
			}
			after := waitVoice(t, service, func(v VoiceState) bool { return !v.Busy && !v.LocalMonitor })
			if after.VoiceConfig != before {
				t.Fatalf("intent not restored: got %+v want %+v", after.VoiceConfig, before)
			}
			if service.GetMicrophoneTest().Enabled || service.GetMicrophoneTest().Busy {
				t.Fatal("test survived stop")
			}
		})
	}
}

func TestOnlineTestStopCancelsPendingStart(t *testing.T) {
	engine := &fakeVoiceEngine{}
	service := voiceService(t, engine)
	entered := make(chan struct{})
	engine.configure = func(ctx context.Context, c audio.VoiceConfig) error {
		if c.LocalMonitor {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	if _, err := service.StartMicrophoneTest(audio.VoiceConfig{Volume: 100}); err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err := service.StopMicrophoneTest(); err != nil {
		t.Fatal(err)
	}
	waitVoice(t, service, func(v VoiceState) bool { return !v.Busy && !v.LocalMonitor && v.Active && v.Muted })
	if service.GetMicrophoneTest().Enabled || service.GetMicrophoneTest().Busy {
		t.Fatal("cancelled monitor returned")
	}
}

func TestOnlineTestFailureRestoresPlaybackWithoutRetryingCapture(t *testing.T) {
	engine := &fakeVoiceEngine{}
	service := voiceService(t, engine)
	engine.configure = func(_ context.Context, c audio.VoiceConfig) error {
		if c.LocalMonitor {
			return errors.New("test device denied")
		}
		if !c.Muted {
			t.Error("failure retried network capture")
		}
		return nil
	}
	if _, err := service.StartMicrophoneTest(audio.VoiceConfig{Volume: 100}); err != nil {
		t.Fatal(err)
	}
	waitVoice(t, service, func(v VoiceState) bool { return !v.Busy && !v.LocalMonitor && v.Active })
	if service.GetMicrophoneTest().Error == "" {
		t.Fatal("test failure hidden")
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

func TestOnlineTestChannelMoveRestoresPendingStop(t *testing.T) {
	engine := &fakeVoiceEngine{}
	service := voiceService(t, engine)
	before := service.GetVoiceState().VoiceConfig
	if _, err := service.StartMicrophoneTest(audio.VoiceConfig{Volume: 100}); err != nil {
		t.Fatal(err)
	}
	waitVoice(t, service, func(v VoiceState) bool { return !v.Busy && v.LocalMonitor })
	// Hold device work so the channel notification supersedes a queued stop.
	service.voiceMu.Lock()
	_, err := service.StopMicrophoneTest()
	if err == nil {
		service.mu.Lock()
		service.state.Session.ChannelID = "20"
		service.voiceChannelChangedLocked()
		service.mu.Unlock()
	}
	service.voiceMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	after := waitVoice(t, service, func(v VoiceState) bool { return !v.Busy && !v.LocalMonitor })
	if after.VoiceConfig != before || service.GetMicrophoneTest().Enabled || service.GetMicrophoneTest().Busy {
		t.Fatal("channel move revived the test or lost original voice intent")
	}
}

func TestOnlineTestShutdownCancelsPendingCapture(t *testing.T) {
	engine := &fakeVoiceEngine{}
	service := voiceService(t, engine)
	entered := make(chan struct{})
	engine.configure = func(ctx context.Context, config audio.VoiceConfig) error {
		if config.LocalMonitor {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	if _, err := service.StartMicrophoneTest(audio.VoiceConfig{Volume: 100}); err != nil {
		t.Fatal(err)
	}
	<-entered
	service.Shutdown()
	if state := service.GetMicrophoneTest(); state.Active || state.Enabled || state.Busy {
		t.Fatalf("test survived shutdown: %+v", state)
	}
	if engine.closed.Load() != 1 {
		t.Fatal("shutdown did not release voice engine exactly once")
	}
}
