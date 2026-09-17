package client

import (
	"errors"
	"testing"

	"github.com/tsukiyoz/resona/internal/audio"
)

type gainEngine struct {
	fakeVoiceEngine
	gain int
	fail bool
}

func (e *gainEngine) SetInputGain(gain int) error {
	if e.fail {
		return errors.New("test failure")
	}
	e.gain = gain
	return nil
}

func TestInputGainPreservesMonitorRestoreAndPrivacy(t *testing.T) {
	for _, online := range []bool{false, true} {
		e := &gainEngine{}
		config := audio.VoiceConfig{Enabled: true, Muted: true, Deafened: true, InputGain: 100, ActivationMode: "ptt"}
		s := &Service{voiceState: VoiceState{VoiceConfig: config}}
		if online {
			s.state.Session.Mode = "connected"
			s.voice = e
			restore := config
			s.onlineTestRestore = &restore
		} else {
			s.microphoneTest = e
		}
		for _, gain := range []int{0, 200, 50} {
			if got, err := s.SetInputGain(gain); err != nil || got != gain {
				t.Fatalf("%d %v", got, err)
			}
		}
		if e.gain != 50 || s.voiceState.InputGain != 50 || s.microphoneTestState.InputGain != 50 {
			t.Fatal("gain not applied")
		}
		if !s.voiceState.Muted || !s.voiceState.Deafened || s.voiceState.ActivationMode != "ptt" || e.changed.Load() != 0 {
			t.Fatal("privacy/device state changed")
		}
		if online && (s.onlineTestRestore.InputGain != 50 || !s.onlineTestRestore.Muted || !s.onlineTestRestore.Deafened) {
			t.Fatal("test restore lost gain or privacy")
		}
		e.fail = true
		if _, err := s.SetInputGain(150); err == nil || s.voiceState.InputGain != 50 {
			t.Fatal("failed gain reported applied")
		}
		e.fail = false
		s.voiceState.Busy = true
		if _, err := s.SetInputGain(100); err == nil || e.gain != 50 {
			t.Fatal("busy transition accepted gain")
		}
	}
}
