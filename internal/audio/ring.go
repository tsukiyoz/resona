package audio

import "sync/atomic"

// sampleRing is a bounded single-producer/single-consumer PCM ring. Audio
// callbacks never wait: overflow drops the newest samples and underflow is silence.
type sampleRing struct {
	data  []float32
	read  atomic.Uint64
	write atomic.Uint64
}

func newSampleRing(samples int) *sampleRing {
	return &sampleRing{data: make([]float32, samples)}
}

func (r *sampleRing) Push(samples []float32) int {
	read, write := r.read.Load(), r.write.Load()
	free := uint64(len(r.data)) - (write - read)
	n := min(len(samples), int(free))
	for i := range n {
		r.data[(write+uint64(i))%uint64(len(r.data))] = samples[i]
	}
	r.write.Store(write + uint64(n))
	return n
}

func (r *sampleRing) Pop(samples []float32) int {
	read, write := r.read.Load(), r.write.Load()
	n := min(len(samples), int(write-read))
	for i := range n {
		samples[i] = r.data[(read+uint64(i))%uint64(len(r.data))]
	}
	r.read.Store(read + uint64(n))
	return n
}

// MixInto has the same single consumer as Pop; no callback allocation or lock.
func (r *sampleRing) MixInto(samples []float32) int {
	read, write := r.read.Load(), r.write.Load()
	n := min(len(samples), int(write-read))
	for i := range n {
		value := samples[i] + r.data[(read+uint64(i))%uint64(len(r.data))]
		samples[i] = max(float32(-1), min(float32(1), value))
	}
	r.read.Store(read + uint64(n))
	return n
}

func (r *sampleRing) Available() int { return int(r.write.Load() - r.read.Load()) }

func (r *sampleRing) Reset() {
	write := r.write.Load()
	r.read.Store(write)
}
