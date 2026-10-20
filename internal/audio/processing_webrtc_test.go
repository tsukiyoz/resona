//go:build cgo

package audio

import (
	"context"
	"math"
	"testing"
)

func TestMissingWebRTCLibraryRejectsSelection(t *testing.T) {
	t.Setenv("RESONA_WEBRTC_PLUGIN_DIR", t.TempDir())
	if AvailableWebRTC() {
		t.Fatal("optional library reported available without a file")
	}
	if err := validateProcessor("preprocess", "aec", ProcessorSpec{Name: "aec", Backend: "webrtc"}); err == nil {
		t.Fatal("accepted WebRTC selection without its native library")
	}
	if rnnoiseAvailable() {
		t.Fatal("RNNoise available without a library")
	}
	if err := validateProcessor("preprocess", "ans", ProcessorSpec{Name: "ans", Backend: "rnnoise"}); err == nil {
		t.Fatal("accepted RNNoise without its native library")
	}
}

func TestOptionalRNNoiseProcessors(t *testing.T) {
	if !rnnoiseAvailable() {
		t.Skip("packaged RNNoise library is not installed beside the test binary")
	}
	spec := ProcessorSpec{Name: "ans", Backend: "rnnoise"}
	if err := validateProcessor("preprocess", "ans", spec); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []ProcessorSpec{
		{Name: "aec", Backend: "rnnoise"},
		{Name: "agc", Backend: "rnnoise"},
		{Name: "ans", Backend: "rnnoise", Params: ProcessorParams{Level: 2}},
	} {
		if err := validateProcessor("preprocess", invalid.Name, invalid); err == nil {
			t.Fatalf("invalid RNNoise selection accepted: %+v", invalid)
		}
	}
	devices := &fakeDeviceFactory{}
	e := newWithFactory(&fakeTransport{codec: CodecOpusVoice}, nil, devices)
	defer e.Close()
	config := VoiceConfig{Enabled: true, Muted: true, Volume: 100}
	if err := e.Configure(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	first := e.run
	for _, backend := range []string{"rnnoise", "speex", "none", "rnnoise"} {
		config.Processing.Preprocess[1] = ProcessorSpec{Name: "ans", Backend: backend}
		if err := e.Configure(context.Background(), config); err != nil {
			t.Fatal(err)
		}
		if e.run != first || len(devices.opens) != 1 || !e.Status().Config.Muted {
			t.Fatal("switching ANS reopened devices or unmuted the microphone")
		}
	}
	config.Processing.Preprocess[1].Params.Level = 3
	if err := e.Configure(context.Background(), config); err == nil || e.Status().Config.Processing.Preprocess[1] != spec {
		t.Fatal("invalid RNNoise parameters changed the applied chain")
	}
}

func TestOptionalWebRTCProcessors(t *testing.T) {
	if !AvailableWebRTC() {
		t.Skip("optional WebRTC library is not installed beside the test binary")
	}
	for _, spec := range []ProcessorSpec{
		{Name: "aec", Backend: "webrtc"},
		{Name: "ans", Backend: "webrtc", Params: ProcessorParams{Level: 4}},
		{Name: "agc", Backend: "webrtc", Params: ProcessorParams{HeadroomDB: 5, MaxGainDB: 18}},
	} {
		t.Run(spec.Name, func(t *testing.T) {
			phase := "preprocess"
			if spec.Name == "agc" {
				phase = "postprocess"
			}
			if err := validateProcessor(phase, spec.Name, spec); err != nil {
				t.Fatal(err)
			}
			processor, err := newProcessor(spec)
			if err != nil {
				t.Fatal(err)
			}
			defer processor.Close()
			frame, reference := make([]float32, FrameSamples), make([]float32, FrameSamples)
			for n := range 20 {
				for i := range frame {
					frame[i] = 0.03 * float32(math.Sin(float64(n*FrameSamples+i)*2*math.Pi*440/SampleRate))
					reference[i] = frame[i] * 0.5
				}
				processor.Process(frame, reference)
				for _, sample := range frame {
					if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
						t.Fatal("native processor returned nonfinite samples")
					}
				}
			}
		})
	}
	for _, spec := range []ProcessorSpec{
		{Name: "aec", Backend: "webrtc", Params: ProcessorParams{TailMS: 200}},
		{Name: "ans", Backend: "webrtc", Params: ProcessorParams{Level: 5}},
		{Name: "agc", Backend: "webrtc", Params: ProcessorParams{HeadroomDB: 21}},
	} {
		if err := validateProcessor("", spec.Name, spec); err == nil {
			t.Errorf("accepted invalid WebRTC parameters: %+v", spec)
		}
	}
}

func BenchmarkWebRTCInputChain(b *testing.B) {
	if !AvailableWebRTC() {
		b.Skip("optional WebRTC library is not installed")
	}
	config := VoiceConfig{Processing: ProcessingConfig{Preprocess: [2]ProcessorSpec{
		{Name: "aec", Backend: "webrtc"}, {Name: "ans", Backend: "webrtc", Params: ProcessorParams{Level: 2}},
	}}}
	processor, err := newSpeechProcessor(config)
	if err != nil {
		b.Fatal(err)
	}
	defer processor.Close()
	input, frame, reference := make([]float32, FrameSamples), make([]float32, FrameSamples), make([]float32, FrameSamples)
	for i := range input {
		input[i] = 0.05 * float32(math.Sin(float64(i)*2*math.Pi*440/SampleRate))
		reference[i] = input[i] * 0.5
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(frame, input)
		processor.Process(frame, reference)
	}
}
