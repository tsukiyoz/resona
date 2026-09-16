package client

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/tsukiyoz/resona/internal/audio"
)

type VoiceState struct {
	audio.VoiceConfig
	Active            bool        `json:"active"`
	ChannelCodec      audio.Codec `json:"channelCodec"`
	Error             string      `json:"error"`
	Busy              bool        `json:"busy"`
	SpeakingClientIDs []string    `json:"speakingClientIDs,omitempty"`
	LocalSpeaking     bool        `json:"localSpeaking"`
	PushToTalkPressed bool        `json:"pushToTalkPressed"`
	InputLevelDB      int         `json:"inputLevelDB"`
}

type voiceEngine interface {
	Configure(context.Context, audio.VoiceConfig) error
	ChannelChanged(context.Context) error
	Status() audio.VoiceState
	Close() error
}

func defaultVoiceConfig() audio.VoiceConfig {
	return audio.VoiceConfig{Muted: true, Volume: 100, InputGain: 100, ActivationMode: "continuous", VADThresholdDB: -40, NoiseSuppression: "off"}
}

func (s *Service) GetVoiceState() VoiceState {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.voiceState
	state.SpeakingClientIDs = []string{}
	if s.state.Session.Mode != "connected" || s.state.Session.SwitchingChannelID != "" || state.Busy || !state.Enabled || !state.Active || state.Deafened {
		state.LocalSpeaking = false
		return state
	}
	state.LocalSpeaking = state.LocalSpeaking && !state.Muted
	for _, id := range s.voiceState.SpeakingClientIDs {
		for _, user := range s.state.Users {
			if user.ID == id && !user.Self && user.ChannelID == s.state.Session.ChannelID {
				state.SpeakingClientIDs = append(state.SpeakingClientIDs, id)
				break
			}
		}
	}
	return state
}

func (s *Service) GetAudioDevices() ([]audio.Device, error) { return audio.Devices() }

func (s *Service) RecordVoiceDiagnostics() {
	s.mu.Lock()
	engine := s.voice
	s.mu.Unlock()
	if source, ok := engine.(interface{ RecordDiagnostics() }); ok {
		source.RecordDiagnostics()
	}
}

// ConfigureVoice returns a pending state; opening devices never blocks the GUI
// command reader. Session/engine epochs suppress late hardware completions.
func (s *Service) ConfigureVoice(config audio.VoiceConfig) (VoiceState, error) {
	return s.configureVoice(config, nil, 0)
}

// ConfigureDefaultVoice opens only playback for the just-connected generation.
func (s *Service) ConfigureDefaultVoice(generation uint64) (VoiceState, error) {
	return s.configureVoice(audio.VoiceConfig{}, &generation, 0)
}

// monitorAction: 0 normal, 1 begin test, 2 end test, 3 reconnect (muted).
func (s *Service) configureVoice(config audio.VoiceConfig, expectedGeneration *uint64, monitorAction int) (VoiceState, error) {
	if err := audio.ValidateConfig(config); err != nil {
		return VoiceState{}, err
	}
	s.mu.Lock()
	if expectedGeneration != nil {
		_, supported := s.connection.(audio.Transport)
		if *expectedGeneration != s.generation || s.state.Session.Mode != "connected" || !supported || s.voice != nil || s.voiceState.Busy {
			state := s.voiceState
			s.mu.Unlock()
			return state, nil
		}
		if monitorAction != 3 {
			config = s.voiceState.VoiceConfig
			config.Enabled, config.Muted, config.Deafened = true, true, false
		}
	}
	if s.shutdown {
		s.mu.Unlock()
		return VoiceState{}, errors.New("客户端已经关闭")
	}
	config.LocalMonitor = false
	if monitorAction == 2 {
		if s.onlineTestRestore == nil {
			state := s.voiceState
			s.mu.Unlock()
			return state, nil
		}
		config = *s.onlineTestRestore
	} else if monitorAction == 1 {
		if s.onlineTestRestore != nil || s.onlineTestStopping {
			s.mu.Unlock()
			return VoiceState{}, errors.New("麦克风试听正在处理，请先停止")
		}
		config.Enabled, config.Muted, config.Deafened, config.LocalMonitor = true, true, false, true
		config.Volume = s.voiceState.Volume
	} else if config.Enabled && (s.onlineTestRestore != nil || s.onlineTestStopping) {
		s.mu.Unlock()
		return VoiceState{}, errors.New("请先停止麦克风试听再调整语音")
	}
	if monitorAction == 2 {
		if s.voiceCancel != nil {
			s.voiceCancel()
		}
		s.suspendVoiceCaptureLocked()
		s.onlineTestRestore = nil
		s.onlineTestStopping = config.Enabled
	}
	if !config.Enabled {
		engine := s.detachVoiceLocked()
		config.Muted = true
		s.voiceState.VoiceConfig = config
		state := s.voiceState
		if engine != nil {
			s.cleanupWG.Add(1)
		}
		s.mu.Unlock()
		if engine != nil {
			go func() {
				defer s.cleanupWG.Done()
				s.closeRetiredVoices()
			}()
		}
		return state, nil
	}
	transport, ok := s.connection.(audio.Transport)
	if !ok || s.state.Session.Mode != "connected" {
		s.mu.Unlock()
		return VoiceState{}, errors.New("当前连接不支持语音")
	}
	if monitorAction != 2 && (s.voiceState.Busy || s.state.Session.SwitchingChannelID != "") {
		s.mu.Unlock()
		return VoiceState{}, errors.New("语音或频道正在切换，请稍候")
	}
	if s.voice == nil && !config.Muted {
		s.mu.Unlock()
		return VoiceState{}, errors.New("请先以麦克风关闭状态启用语音")
	}
	if monitorAction == 1 {
		restore := s.voiceState.VoiceConfig
		s.onlineTestRestore = &restore
		s.microphoneTestState = VoiceState{InputLevelDB: -60}
		s.suspendVoiceCaptureLocked()
	}
	epoch, generation := s.voiceEpoch, s.generation
	s.voiceOperation++
	operation := s.voiceOperation
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	s.voiceCancel = cancel
	s.voiceState.VoiceConfig, s.voiceState.Busy, s.voiceState.Error = config, true, ""
	s.notifyChangedLocked()
	state := s.voiceState
	s.cleanupWG.Add(1)
	if monitorAction == 1 || monitorAction == 2 {
		s.cleanupWG.Add(1)
	}
	s.mu.Unlock()
	if monitorAction == 1 || monitorAction == 2 {
		go func() {
			defer s.cleanupWG.Done()
			<-ctx.Done()
			if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return
			}
			s.mu.Lock()
			if epoch != s.voiceEpoch || generation != s.generation || operation != s.voiceOperation || !s.voiceState.Busy {
				s.mu.Unlock()
				return
			}
			s.suspendVoiceCaptureLocked()
			s.detachVoiceLocked()
			s.microphoneTestState.Error = "麦克风试听设备操作超时，语音已停用，请检查设备后重试"
			s.notifyChangedLocked()
			s.mu.Unlock()
			s.closeRetiredVoices()
		}()
	}
	go func() {
		defer s.cleanupWG.Done()
		defer cancel()
		s.voiceMu.Lock()
		defer s.voiceMu.Unlock()
		s.drainRetiredVoices()
		s.mu.Lock()
		if epoch != s.voiceEpoch || generation != s.generation || operation != s.voiceOperation {
			s.mu.Unlock()
			return
		}
		if ctx.Err() != nil {
			s.voiceOperationExpiredLocked()
			s.mu.Unlock()
			return
		}
		if s.voice == nil {
			factory := s.newVoice
			if factory == nil {
				factory = func(t audio.Transport, update func(audio.VoiceState)) voiceEngine { return audio.New(t, update) }
			}
			s.voice = factory(transport, func(next audio.VoiceState) { s.applyVoiceState(epoch, generation, next) })
			s.syncUserPlaybackLocked()
		}
		engine := s.voice
		s.mu.Unlock()
		err := engine.Configure(ctx, config)
		// A failed test may have stopped playback. Recover output once, without
		// reopening the denied capture device or restoring network transmission.
		if err != nil && monitorAction == 1 && ctx.Err() == nil {
			s.mu.Lock()
			var restore *audio.VoiceConfig
			if epoch == s.voiceEpoch && generation == s.generation && operation == s.voiceOperation && s.onlineTestRestore != nil {
				copy := *s.onlineTestRestore
				copy.Muted, copy.LocalMonitor = true, false
				restore = &copy
			}
			s.mu.Unlock()
			if restore != nil {
				_ = engine.Configure(ctx, *restore)
			}
		}
		next := engine.Status()
		s.mu.Lock()
		defer s.mu.Unlock()
		if epoch != s.voiceEpoch || generation != s.generation || operation != s.voiceOperation {
			return
		}
		s.voiceCancel = nil
		s.voiceState = voiceSnapshot(next)
		if monitorAction == 2 || (monitorAction == 1 && err != nil) {
			s.onlineTestRestore = nil
			s.onlineTestStopping = false
			s.microphoneTestState = VoiceState{InputLevelDB: -60}
			if err != nil {
				s.microphoneTestState.Error = "麦克风试听失败，请检查设备和权限后重试"
			}
		}
		if err != nil && s.voiceState.Error == "" {
			s.voiceState.Error = "语音设备启动失败，请检查设备和麦克风权限"
		}
		s.notifyChangedLocked()
	}()
	return state, nil
}

func voiceSnapshot(next audio.VoiceState) VoiceState {
	ids := make([]string, 0, len(next.SpeakingClientIDs))
	for _, id := range next.SpeakingClientIDs {
		ids = append(ids, strconv.FormatUint(uint64(id), 10))
	}
	return VoiceState{VoiceConfig: next.Config, Active: next.Active, ChannelCodec: next.ChannelCodec, Error: next.Error, SpeakingClientIDs: ids, LocalSpeaking: next.LocalSpeaking, PushToTalkPressed: next.PushToTalkPressed, InputLevelDB: next.InputLevelDB}
}

func (s *Service) applyVoiceState(epoch, generation uint64, _ audio.VoiceState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if epoch != s.voiceEpoch || generation != s.generation || s.voice == nil {
		return
	}
	busy := s.voiceState.Busy
	if busy {
		// Superseded engine operations must not overwrite their replacement's pending state.
		return
	}
	// Engine notifications are asynchronous; read the current state so a queued
	// earlier configuration cannot overwrite a newer completed operation.
	s.voiceState = voiceSnapshot(s.voice.Status())
	s.voiceState.Busy = busy
	s.notifyChangedLocked()
}

func (s *Service) detachVoiceLocked() voiceEngine {
	s.suspendVoiceCaptureLocked()
	s.onlineTestRestore = nil
	s.onlineTestStopping = false
	s.microphoneTestState = VoiceState{InputLevelDB: -60}
	if s.voiceCancel != nil {
		s.voiceCancel()
		s.voiceCancel = nil
	}
	s.voiceEpoch++
	s.voiceOperation++
	engine := s.voice
	if engine != nil {
		s.retiredVoices = append(s.retiredVoices, engine)
	}
	s.voice = nil
	config := s.voiceState.VoiceConfig
	config.Enabled, config.Muted, config.Deafened, config.LocalMonitor = false, true, false, false
	s.voiceState = VoiceState{VoiceConfig: config}
	s.notifyChangedLocked()
	return engine
}

func (s *Service) suspendVoiceCaptureLocked() {
	if control, ok := s.voice.(interface{ SuspendCapture() }); ok {
		control.SuspendCapture()
	}
}

func (s *Service) closeRetiredVoices() {
	s.voiceMu.Lock()
	defer s.voiceMu.Unlock()
	s.drainRetiredVoices()
}

// voiceMu serializes retirement before a replacement installs its packet handler.
func (s *Service) drainRetiredVoices() {
	s.mu.Lock()
	retired := s.retiredVoices
	s.retiredVoices = nil
	s.mu.Unlock()
	for _, engine := range retired {
		_ = engine.Close()
	}
}

// Caller holds s.mu. Device reconfiguration must not run in a protocol callback.
func (s *Service) voiceChannelChangedLocked() {
	if s.voice == nil || !s.voiceState.Enabled {
		return
	}
	engine, epoch, generation := s.voice, s.voiceEpoch, s.generation
	var restore *audio.VoiceConfig
	if s.onlineTestRestore != nil || s.onlineTestStopping {
		copy := s.voiceState.VoiceConfig
		if s.onlineTestRestore != nil {
			copy = *s.onlineTestRestore
		}
		restore = &copy
		s.suspendVoiceCaptureLocked()
		s.onlineTestRestore = nil
		s.onlineTestStopping = false
		s.microphoneTestState = VoiceState{InputLevelDB: -60}
		s.voiceState.VoiceConfig = copy
	}
	s.voiceOperation++
	operation := s.voiceOperation
	if s.voiceCancel != nil {
		s.voiceCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	s.voiceCancel = cancel
	s.voiceState.Busy = true
	s.cleanupWG.Add(1)
	go func() {
		defer s.cleanupWG.Done()
		defer cancel()
		s.voiceMu.Lock()
		defer s.voiceMu.Unlock()
		if ctx.Err() != nil {
			s.mu.Lock()
			if epoch == s.voiceEpoch && generation == s.generation && operation == s.voiceOperation {
				s.voiceOperationExpiredLocked()
			}
			s.mu.Unlock()
			return
		}
		if restore != nil {
			_ = engine.Configure(ctx, *restore)
		} else {
			_ = engine.ChannelChanged(ctx)
		}
		next := engine.Status()
		s.mu.Lock()
		defer s.mu.Unlock()
		if epoch != s.voiceEpoch || generation != s.generation || operation != s.voiceOperation {
			return
		}
		s.voiceState = voiceSnapshot(next)
		s.voiceCancel = nil
		s.notifyChangedLocked()
	}()
}

func (s *Service) voiceOperationExpiredLocked() {
	if s.onlineTestRestore != nil || s.onlineTestStopping {
		s.detachVoiceLocked()
		s.microphoneTestState.Error = "麦克风试听设备操作超时，语音已停用，请检查设备后重试"
		s.cleanupWG.Add(1)
		go func() {
			defer s.cleanupWG.Done()
			s.closeRetiredVoices()
		}()
	}
	s.voiceCancel = nil
	s.voiceState.Busy = false
	s.voiceState.Error = "语音设备操作超时，请检查设备和麦克风权限后重试"
	s.notifyChangedLocked()
}
