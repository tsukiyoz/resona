package client

import (
	"errors"
	"testing"
)

type outputGainEngine struct {
	fakeVoiceEngine
	volume int
	fail   bool
}

func (e *outputGainEngine) SetOutputGain(volume int) error {
	if e.fail {
		return errors.New("busy")
	}
	e.volume = volume
	return nil
}

func TestOutputGainCommitsOnlyAfterEngineSuccess(t *testing.T) {
	e := &outputGainEngine{}
	s := &Service{voice: e, state: Workspace{Session: Session{ID: "one", Mode: "connected"}}}
	s.voiceState.Volume = 100
	if got, err := s.SetOutputGain("one", 794); err != nil || got != 794 || e.volume != 794 {
		t.Fatal(got, err)
	}
	e.fail = true
	if _, err := s.SetOutputGain("one", 200); err == nil || s.voiceState.Volume != 794 {
		t.Fatal("failed gain committed")
	}
	e.fail = false
	if _, err := s.SetOutputGain("old", 200); err == nil || e.volume != 794 {
		t.Fatal("stale session changed output")
	}
	for _, volume := range []int{-1, 795} {
		if _, err := s.SetOutputGain("one", volume); err == nil {
			t.Fatal("invalid gain accepted")
		}
	}
	s.voiceState.Busy = true
	if _, err := s.SetOutputGain("one", 200); err == nil {
		t.Fatal("lifecycle work was bypassed")
	}
}
