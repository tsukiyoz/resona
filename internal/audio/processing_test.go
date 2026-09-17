package audio

import (
	"errors"
	"testing"
)

type chainProbe struct{ closed *int }

func (*chainProbe) Process([]float32, []float32) {}
func (p *chainProbe) Close()                     { *p.closed++ }

func TestBuildChainReleasesPreparedProcessorsOnFailure(t *testing.T) {
	closed := 0
	specs := []ProcessorSpec{{Name: "aec", Backend: "speex"}, {Name: "ans", Backend: "speex"}}
	chain, err := BuildChain(specs, func(spec ProcessorSpec) (speechProcessor, error) {
		if spec.Name == "ans" {
			return nil, errors.New("allocation denied")
		}
		return &chainProbe{closed: &closed}, nil
	})
	if err == nil || chain != nil || closed != 1 {
		t.Fatalf("partial chain escaped: chain=%v err=%v closed=%d", chain, err, closed)
	}
}

func TestProcessingRejectsWrongSlotAndDisabledParameters(t *testing.T) {
	for _, config := range []ProcessingConfig{
		{Preprocess: [2]ProcessorSpec{{Name: "agc", Backend: "speex"}}},
		{Postprocess: [1]ProcessorSpec{{Name: "ans", Backend: "speex"}}},
		{Preprocess: [2]ProcessorSpec{{Name: "aec", Backend: "speex", Params: ProcessorParams{Level: 3}}}},
		{Postprocess: [1]ProcessorSpec{{Name: "agc", Backend: "unknown"}}},
	} {
		if err := validateProcessing(config); err == nil {
			t.Fatalf("accepted invalid chain %+v", config)
		}
	}
}

func TestProcessorOptionsMatchAvailableBackends(t *testing.T) {
	options := ProcessorOptions()
	for _, slot := range []struct{ phase, name string }{
		{"preprocess", "aec"}, {"preprocess", "ans"}, {"postprocess", "agc"},
	} {
		found := map[string]bool{}
		for _, option := range options {
			if option.Phase == slot.phase && option.Name == slot.name {
				if option.DisplayName == "" || option.Description == "" || found[option.ID] {
					t.Fatalf("invalid option: %+v", option)
				}
				found[option.ID] = true
			}
		}
		if !found["none"] || found["speex"] != speexAvailable || found["webrtc"] != AvailableWebRTC() {
			t.Fatalf("wrong available options for %s/%s: %v", slot.phase, slot.name, found)
		}
	}
}
