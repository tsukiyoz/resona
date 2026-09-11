package desktopipc

import (
	"encoding/json"
	"github.com/tsukiyoz/resona/internal/client"
	"testing"
)

func TestInputGainWireDefaultsAndExplicitZero(t *testing.T) {
	s, err := client.New(&memoryProfiles{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown()
	for _, tc := range []struct {
		params string
		want   int
	}{{`{}`, 100}, {`{"inputGain":0}`, 0}, {`{"inputGain":200}`, 200}} {
		if _, err := dispatch(s, request{Method: "SetVoicePreferences", Params: json.RawMessage(tc.params)}); err != nil {
			t.Fatal(err)
		}
		if got := s.GetVoiceState().InputGain; got != tc.want {
			t.Fatalf("gain=%d want=%d", got, tc.want)
		}
	}
	for _, params := range []string{`{}`, `{"inputGain":null}`, `{"inputGain":201}`, `{"inputGain":-1}`} {
		if _, err := dispatch(s, request{Method: "SetInputGain", Params: json.RawMessage(params)}); err == nil {
			t.Fatalf("accepted %s", params)
		}
	}
	if _, err := dispatch(s, request{Method: "SetInputGain", Params: json.RawMessage(`{"inputGain":0}`)}); err != nil || s.GetVoiceState().InputGain != 0 {
		t.Fatalf("zero rejected: %v", err)
	}
}
