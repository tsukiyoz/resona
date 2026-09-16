package audio

// A frame-peak limiter preserves relative speaker levels without allocating or
// adding buffering. Attenuation is immediate; release is about 200 ms at 20 ms/frame.
type outputLimiter struct{ gain float32 }

func (l *outputLimiter) apply(samples []float32, duck float32) {
	peak := float32(0)
	for _, sample := range samples {
		v := sample * duck
		if v < 0 {
			v = -v
		}
		peak = max(peak, v)
	}
	target := float32(1)
	if peak > 0.98 {
		target = 0.98 / peak
	}
	if l.gain == 0 || target < l.gain {
		l.gain = target
	} else {
		l.gain += (target - l.gain) * 0.1
	}
	for i := range samples {
		samples[i] *= duck * l.gain
	}
}
