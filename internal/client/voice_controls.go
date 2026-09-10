package client

import (
	"context"
	"errors"
	"time"

	"github.com/tsukiyoz/resona/internal/audio"
)

// SetVoicePreferences prepares the next connection without opening devices.
func (s *Service) SetVoicePreferences(config audio.VoiceConfig) (VoiceState, error) {
	if err := audio.ValidateConfig(config); err != nil {
		return VoiceState{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown {
		return VoiceState{}, errors.New("客户端已经关闭")
	}
	if s.remoteActiveLocked() || s.voiceState.Busy {
		return VoiceState{}, errors.New("请在离线状态保存语音偏好")
	}
	config.Enabled, config.Muted, config.Deafened = false, true, false
	s.voiceState = VoiceState{VoiceConfig: config, InputLevelDB: -60}
	s.notifyChangedLocked()
	return s.voiceState, nil
}

func (s *Service) SetPushToTalk(pressed bool) (VoiceState, error) {
	s.mu.Lock()
	engine := s.voice
	allowed := !s.shutdown && s.onlineTestRestore == nil && !s.onlineTestStopping && s.state.Session.Mode == "connected" && !s.voiceState.Busy && !s.voiceState.Muted && !s.voiceState.Deafened
	s.mu.Unlock()
	if control, ok := engine.(interface{ SetPushToTalk(bool) error }); ok {
		if err := control.SetPushToTalk(pressed && allowed); err != nil && pressed {
			return s.GetVoiceState(), err
		}
		next := engine.Status()
		s.mu.Lock()
		if s.voice == engine {
			s.voiceState.PushToTalkPressed = next.PushToTalkPressed
			s.voiceState.LocalSpeaking = next.LocalSpeaking
			s.notifyChangedLocked()
		}
		s.mu.Unlock()
	}
	return s.GetVoiceState(), nil
}

func (s *Service) GetMicrophoneTest() VoiceState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.onlineTestRestore != nil || s.onlineTestStopping {
		state := s.voiceState
		state.Enabled = s.onlineTestRestore != nil
		state.Active = state.Active && state.Enabled && !state.Busy
		state.Muted, state.Deafened = false, false
		state.LocalSpeaking = false
		state.SpeakingClientIDs = nil
		return state
	}
	return s.microphoneTestState
}

func (s *Service) StartMicrophoneTest(config audio.VoiceConfig) (VoiceState, error) {
	if err := audio.ValidateConfig(config); err != nil {
		return VoiceState{}, err
	}
	s.mu.Lock()
	if s.state.Session.Mode == "connected" {
		s.mu.Unlock()
		_, err := s.configureVoice(config, nil, 1)
		return s.GetMicrophoneTest(), err
	}
	if s.shutdown {
		s.mu.Unlock()
		return VoiceState{}, errors.New("客户端已经关闭")
	}
	if s.state.Session.Mode == "connecting" || s.state.Session.Mode == "disconnecting" {
		s.mu.Unlock()
		return VoiceState{}, errors.New("连接正在切换，请稍后进行麦克风试听")
	}
	if s.microphoneTest != nil {
		s.mu.Unlock()
		return VoiceState{}, errors.New("麦克风试听已启动，请先停止")
	}
	config.Enabled, config.Muted, config.Deafened = true, false, false
	factory := s.newMicrophoneTest
	if factory == nil {
		factory = func(notify func(audio.VoiceState)) voiceEngine { return audio.NewMicrophoneTest(notify) }
	}
	var engine voiceEngine
	engine = factory(func(audio.VoiceState) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.microphoneTest != engine {
			return
		}
		busy := s.microphoneTestState.Busy
		s.microphoneTestState = voiceSnapshot(engine.Status())
		s.microphoneTestState.Busy = busy
		s.notifyChangedLocked()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	s.microphoneTest, s.microphoneTestCancel = engine, cancel
	s.microphoneTestState = VoiceState{VoiceConfig: config, Busy: true, InputLevelDB: -60}
	state := s.microphoneTestState
	s.cleanupWG.Add(2)
	s.notifyChangedLocked()
	s.mu.Unlock()
	go func() {
		defer s.cleanupWG.Done()
		<-ctx.Done()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			s.expireMicrophoneTest(engine)
		}
	}()
	go func() {
		defer s.cleanupWG.Done()
		defer cancel()
		s.microphoneTestMu.Lock()
		defer s.microphoneTestMu.Unlock()
		s.drainMicrophoneTests()
		err := ctx.Err()
		if err == nil {
			err = engine.Configure(ctx, config)
		}
		next := engine.Status()
		s.mu.Lock()
		if s.microphoneTest != engine {
			s.mu.Unlock()
			return
		}
		s.microphoneTestState = voiceSnapshot(next)
		if err != nil {
			s.microphoneTestState.Error = err.Error()
			s.microphoneTest = nil
		}
		s.microphoneTestCancel = nil
		s.notifyChangedLocked()
		s.mu.Unlock()
		if err != nil {
			_ = engine.Close()
		}
	}()
	return state, nil
}

// Native device calls may outlive context cancellation. Detach their state now;
// serialized retirement closes the device when the native call returns.
func (s *Service) expireMicrophoneTest(engine voiceEngine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.microphoneTest != engine || !s.microphoneTestState.Busy {
		return
	}
	s.stopMicrophoneTestLocked()
	s.microphoneTestState.Error = "麦克风设备启动超时，请检查设备后重试"
	s.notifyChangedLocked()
}

func (s *Service) StopMicrophoneTest() (VoiceState, error) {
	s.mu.Lock()
	if s.onlineTestRestore != nil || s.onlineTestStopping {
		s.mu.Unlock()
		_, err := s.configureVoice(audio.VoiceConfig{}, nil, 2)
		return s.GetMicrophoneTest(), err
	}
	defer s.mu.Unlock()
	s.stopMicrophoneTestLocked()
	return s.microphoneTestState, nil
}

// Caller holds s.mu. Shutdown waits for cleanupWG after detaching this owner.
func (s *Service) stopMicrophoneTestLocked() {
	if s.onlineTestRestore != nil || s.onlineTestStopping {
		s.suspendVoiceCaptureLocked()
		s.onlineTestRestore = nil
		s.onlineTestStopping = false
	}
	if s.microphoneTestCancel != nil {
		s.microphoneTestCancel()
		s.microphoneTestCancel = nil
	}
	engine := s.microphoneTest
	s.microphoneTest = nil
	s.microphoneTestState = VoiceState{InputLevelDB: -60}
	if engine == nil {
		return
	}
	s.retiredMicrophoneTests = append(s.retiredMicrophoneTests, engine)
	s.cleanupWG.Add(1)
	s.notifyChangedLocked()
	go func() {
		defer s.cleanupWG.Done()
		s.microphoneTestMu.Lock()
		defer s.microphoneTestMu.Unlock()
		s.drainMicrophoneTests()
	}()
}

// microphoneTestMu orders retirement before another test opens devices.
func (s *Service) drainMicrophoneTests() {
	s.mu.Lock()
	retired := s.retiredMicrophoneTests
	s.retiredMicrophoneTests = nil
	s.mu.Unlock()
	for _, engine := range retired {
		_ = engine.Close()
	}
}
