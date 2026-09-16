package client

import (
	"errors"
	"github.com/tsukiyoz/resona/internal/audio"
)

func (s *Service) SetOutputGain(sessionID string, volume int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if volume < 0 || volume > audio.MaxPlaybackVolume {
		return 0, errors.New("收听增益超出范围")
	}
	if sessionID != s.state.Session.ID || s.shutdown || s.voiceState.Busy || s.microphoneTestState.Enabled || s.microphoneTestState.Busy || s.onlineTestRestore != nil || s.onlineTestStopping || s.state.Session.SwitchingChannelID != "" {
		return 0, errors.New("音频或会话正在切换，请稍后重试")
	}
	switch s.state.Session.Mode {
	case "connecting", "reconnecting", "disconnecting":
		return 0, errors.New("连接正在切换，请稍后重试")
	}
	if s.voice != nil {
		control, ok := s.voice.(interface{ SetOutputGain(int) error })
		if !ok {
			return 0, errors.New("当前音频引擎不支持收听增益")
		}
		if err := control.SetOutputGain(volume); err != nil {
			return 0, err
		}
	}
	s.voiceState.Volume = volume
	s.notifyChangedLocked()
	return volume, nil
}
