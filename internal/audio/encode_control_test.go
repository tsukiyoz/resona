package audio

import (
	"testing"
	"time"
)

type refillingProcessor struct {
	run    *engineRun
	update encodeUpdate
	calls  int
	closed bool
}

func (p *refillingProcessor) Process(samples, _ []float32) {
	p.calls++
	// Keep the queue nonempty without relying on producer scheduling or speed.
	p.run.capturePCM.Push(samples)
	if p.calls == 1 {
		p.run.encodeControl <- p.update
	}
}

func (p *refillingProcessor) Close() { p.closed = true }

func TestEncodeControlWhileCaptureRemainsNonempty(t *testing.T) {
	r, err := newEngineRun(&Engine{monitor: true}, CodecOpusVoice, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer r.stop()
	// Enqueue the update from inside Process, after the outer select has run.
	r.encodeControl = make(chan encodeUpdate, 1)
	ack := make(chan struct{}, 1)
	p := &refillingProcessor{run: r, update: encodeUpdate{ack: ack}}
	r.processor = p
	r.allowSend.Store(true)
	r.lastMeter = time.Now().Add(time.Hour)
	r.capturePCM.Push(make([]float32, FrameSamples))
	r.captureWake <- struct{}{}
	r.start()
	select {
	case <-ack:
		if p.calls != 1 || !p.closed {
			t.Fatalf("replacement missed frame boundary: calls=%d closed=%v", p.calls, p.closed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nonempty capture queue starved processor replacement")
	}
}
