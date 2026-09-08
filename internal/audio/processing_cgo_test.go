//go:build cgo

package audio

import (
	"context"
	"math"
	"testing"
)

func BenchmarkIdlePlaybackCallback(b *testing.B) {
	r := &engineRun{ctx: context.Background(), playbackPCM: newSampleRing(FrameSamples * 2 * pcmBufferFrames)}
	output := make([]float32, FrameSamples*2)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.playback(output)
	}
}

func BenchmarkSpeechProcessing(b *testing.B) {
	processor, err := newSpeechProcessor(VoiceConfig{NoiseSuppression: "high", EchoCancellation: true, EchoSuppression: true})
	if err != nil {
		b.Fatal(err)
	}
	defer processor.Close()
	input, frame, reference := make([]float32, FrameSamples), make([]float32, FrameSamples), make([]float32, FrameSamples)
	for i := range input {
		input[i] = float32(math.Sin(float64(i)*2*math.Pi*440/SampleRate)) * 0.1
		reference[i] = input[i] * 0.5
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(frame, input)
		processor.Process(frame, reference)
	}
}

func TestSpeexSuppressesStationaryNoiseAndEcho(t *testing.T) {
	for _, echo := range []bool{false, true} {
		name := "stationary noise"
		config := VoiceConfig{NoiseSuppression: "high"}
		if echo {
			name = "delayed echo"
			config = VoiceConfig{EchoCancellation: true, EchoSuppression: true}
		}
		t.Run(name, func(t *testing.T) {
			processor, err := newSpeechProcessor(config)
			if err != nil {
				t.Fatal(err)
			}
			defer processor.Close()
			microphone, reference, previous := make([]float32, FrameSamples), make([]float32, FrameSamples), make([]float32, FrameSamples)
			seed := uint32(42)
			var before, after float64
			for frame := 0; frame < 350; frame++ {
				for i := range reference {
					seed = seed*1664525 + 1013904223
					reference[i] = (float32(int32(seed)) / 2147483648) * 0.12
					microphone[i] = reference[i]
					if echo {
						microphone[i] = previous[i] * 0.5
					}
					if frame >= 300 {
						before += float64(microphone[i] * microphone[i])
					}
				}
				processor.Process(microphone, reference)
				copy(previous, reference)
				if frame >= 300 {
					for _, sample := range microphone {
						after += float64(sample * sample)
					}
				}
			}
			ratio := math.Sqrt(after / before)
			if ratio >= 0.65 {
				t.Fatalf("DSP attenuation ineffective: RMS ratio %.3f", ratio)
			}
			t.Logf("processed/input RMS ratio %.4f", ratio)
		})
	}
}
