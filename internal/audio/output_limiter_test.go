package audio

import (
	"math"
	"testing"
)

func TestOutputGainStackingAndLimiter(t *testing.T) {
	// Global +6 dB (~2x) and peer +6 dB (~2x) multiply before limiting.
	peers := map[uint16]PeerPlayback{1: {Instance: "a", Volume: 200}}
	peer, _ := peerGain(&peers, 1, "a")
	samples := []float32{0.05 * 2 * peer, -0.1 * 2 * peer}
	var limiter outputLimiter
	limiter.apply(samples, 1)
	if samples[0] != 0.2 || samples[1] != -0.4 {
		t.Fatal(samples)
	}
	loud := []float32{2, -4, 1, -2}
	limiter.apply(loud, 1)
	if math.Abs(float64(loud[0]/loud[1]+0.5)) > 1e-6 {
		t.Fatal("waveform ratio changed")
	}
	for _, sample := range loud {
		if sample > 0.981 || sample < -0.981 {
			t.Fatal(sample)
		}
	}
	before := limiter.gain
	limiter.apply([]float32{0.1}, 1)
	if limiter.gain <= before || limiter.gain >= 1 {
		t.Fatal("release must be gradual")
	}
	peers[1] = PeerPlayback{Instance: "a", Volume: MaxPlaybackVolume, Muted: true}
	if gain, _ := peerGain(&peers, 1, "a"); gain != 0 {
		t.Fatal("gain bypassed mute")
	}
}

func BenchmarkOutputLimiter(b *testing.B) {
	samples := make([]float32, FrameSamples*2)
	var limiter outputLimiter
	b.ReportAllocs()
	for b.Loop() {
		limiter.apply(samples, 1)
	}
}
