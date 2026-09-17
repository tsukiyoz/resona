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
