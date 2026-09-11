package client

import "errors"

// Engine setters are bounded, do not open devices, and notify asynchronously.
func (s *Service) SetInputGain(gain int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if gain < 0 || gain > 200 {
		return 0, errors.New("麦克风输入增益必须在 0 到 200 之间")
	}
	if s.shutdown || s.voiceState.Busy || s.microphoneTestState.Busy || s.onlineTestStopping || s.state.Session.SwitchingChannelID != "" || s.state.Session.Mode == "connecting" || s.state.Session.Mode == "disconnecting" {
		return 0, errors.New("音频或连接正在切换，请稍后重试")
	}
	engine := s.voice
	if s.microphoneTest != nil {
		engine = s.microphoneTest
	}
	if engine != nil {
		control, ok := engine.(interface{ SetInputGain(int) error })
		if !ok {
			return 0, errors.New("当前音频引擎不支持输入增益")
		}
		if err := control.SetInputGain(gain); err != nil {
			return 0, err
		}
	}
	s.voiceState.InputGain = gain
	s.microphoneTestState.InputGain = gain
	if s.onlineTestRestore != nil {
		s.onlineTestRestore.InputGain = gain
	}
	s.notifyChangedLocked()
	return gain, nil
}
