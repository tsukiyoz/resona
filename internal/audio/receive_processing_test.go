//go:build cgo

package audio

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"
)

func receiveTone(frame []float32, amplitude float32) {
	for i := range frame {
		frame[i] = amplitude * float32(math.Sin(2*math.Pi*147*float64(i)/SampleRate))
	}
}

func TestReceiveAGCIndependentHistoriesAndLifecycle(t *testing.T) {
	c := receiveChain{}
	defer c.close()
	now := time.Now()
	processing := ProcessingConfig{Postprocess: [1]ProcessorSpec{{Name: "agc", Backend: "speex"}}}
	quiet, err := c.peer(1, "a", processing, now)
	if err != nil {
		t.Fatal(err)
	}
	loud, err := c.peer(2, "b", processing, now)
	if err != nil {
		t.Fatal(err)
	}
	control, err := newPlaybackProcessor()
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	frame, reference, other := make([]float32, FrameSamples), make([]float32, FrameSamples), make([]float32, FrameSamples)
	for i := 0; i < 500; i++ {
		receiveTone(frame, 0.02)
		copy(reference, frame)
		receiveTone(other, 0.5)
		quiet.process(frame, false)
		loud.process(other, false)
		control.Process(reference, nil)
		for j := range frame {
			if frame[j] != reference[j] {
				t.Fatal("another receiver changed quiet user's gain history")
			}
		}
	}
	if inputLevelDB(frame) <= -37 {
		t.Fatalf("quiet input was not amplified: %d dB", inputLevelDB(frame))
	}
	same, _ := c.peer(1, "a", processing, now.Add(time.Second))
	if same != quiet {
		t.Fatal("talk pause reset gain history")
	}
	replacement, _ := c.peer(1, "new-instance", processing, now.Add(2*time.Second))
	if replacement == quiet {
		t.Fatal("member identity reused gain history")
	}
	c.prune(now.Add(3*time.Second), func(id uint16, instance string) bool { return id == 1 })
	if len(c.peers) != 1 {
		t.Fatal("departed member retained")
	}
	c.prune(now.Add(time.Minute), func(uint16, string) bool { return true })
	if len(c.peers) != 0 {
		t.Fatal("idle history retained indefinitely")
	}
}

type countingProcessor struct{ calls int }

func (p *countingProcessor) Process(samples, _ []float32) {
	p.calls++
	for i := range samples {
		samples[i] *= 2
	}
}
func (*countingProcessor) Close() {}

func TestReceiveAGCFreezesOnSilenceAndConcealment(t *testing.T) {
	backend := &countingProcessor{}
	p := &receiveProcessor{processor: backend}
	frame := make([]float32, FrameSamples)
	p.process(frame, false)
	receiveTone(frame, .1)
	p.process(frame, true)
	if backend.calls != 0 {
		t.Fatal("silence or PLC trained AGC")
	}
	p.process(frame, false)
	if backend.calls != 1 {
		t.Fatal("valid audio bypassed AGC")
	}
}

func TestReceiveAGCUpdatePreservesCaptureProcessorAndDevices(t *testing.T) {
	devices := &fakeDeviceFactory{}
	e := newWithFactory(&fakeTransport{codec: CodecOpusVoice}, nil, devices)
	defer e.Close()
	config := VoiceConfig{Enabled: true, Muted: true, Volume: 100, InputGain: 100}
	config.Processing.Preprocess[0] = ProcessorSpec{Name: "aec", Backend: "speex"}
	if err := e.Configure(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	processor := e.run.processor
	for _, mode := range []string{"speex", "none", "speex"} {
		config.Processing.Postprocess[0] = ProcessorSpec{Name: "agc", Backend: mode}
		if err := e.Configure(context.Background(), config); err != nil {
			t.Fatal(err)
		}
		if e.run.processor != processor || len(devices.opens) != 1 {
			t.Fatal("receive-only update reset capture/device state")
		}
	}
	config.Processing.Postprocess[0].Backend = "unavailable"
	if e.Configure(context.Background(), config) == nil || e.Status().Config.Processing.Postprocess[0].Backend != "speex" {
		t.Fatal("invalid mode changed applied configuration")
	}
	config.Processing.Postprocess[0].Backend = "speex"
	config.Processing.Preprocess[0] = ProcessorSpec{Name: "aec", Backend: "none", Params: ProcessorParams{Residual: true}}
	if e.Configure(context.Background(), config) == nil || e.Status().Config.Processing.Preprocess[0].Backend != "speex" {
		t.Fatal("invalid residual-only request changed applied AEC configuration")
	}
}

func TestResidualEchoSuppressionRequiresAEC(t *testing.T) {
	for _, choice := range []struct {
		aec, residual, valid bool
	}{
		{false, false, true},
		{true, false, true},
		{true, true, true},
		{false, true, false},
	} {
		config := VoiceConfig{}
		if choice.aec {
			config.Processing.Preprocess[0] = ProcessorSpec{Name: "aec", Backend: "speex", Params: ProcessorParams{Residual: choice.residual}}
		} else if choice.residual {
			config.Processing.Preprocess[0] = ProcessorSpec{Name: "aec", Backend: "none", Params: ProcessorParams{Residual: true}}
		}
		if err := ValidateConfig(config); (err == nil) != choice.valid {
			t.Fatalf("AEC=%v residual=%v: validation error %v", choice.aec, choice.residual, err)
		}
	}
	config := VoiceConfig{Processing: ProcessingConfig{Preprocess: [2]ProcessorSpec{{Name: "aec", Backend: "speex", Params: ProcessorParams{Residual: true}}, {Name: "ans", Backend: "speex", Params: ProcessorParams{Level: 2}}}}}
	if err := ValidateConfig(config); err != nil {
		t.Fatal(err)
	}
}

func TestReceiveProcessingPrecedesManualGain(t *testing.T) {
	processed, err := newSpeaker(CodecOpusVoice)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := newSpeaker(CodecOpusVoice)
	if err != nil {
		t.Fatal(err)
	}
	backend := &countingProcessor{}
	processed.receive = &receiveProcessor{processor: backend}
	now := time.Now()
	data := encodedTonePacket(t, CodecOpusVoice)
	for seq := uint16(0); seq < 4; seq++ {
		packet := Packet{Sequence: seq, Codec: CodecOpusVoice, Data: data, ReceivedAt: now}
		processed.jitter.Push(packet)
		plain.jitter.Push(packet)
	}
	processed.lastReceived, plain.lastReceived = now, now
	a, b := make([]float32, FrameSamples*2), make([]float32, FrameSamples*2)
	for i := 0; i < 3; i++ {
		clear(a)
		clear(b)
		if _, _, _, _, err := processed.render(a, .25, now); err != nil {
			t.Fatal(err)
		}
		if _, _, _, _, err := plain.render(b, .5, now); err != nil {
			t.Fatal(err)
		}
		for j := range a {
			if a[j] != b[j] {
				t.Fatal("manual gain did not compose after receiver processing")
			}
		}
	}
	if backend.calls == 0 {
		t.Fatal("decoded PCM never reached receive chain")
	}
}

func BenchmarkReceiveAGC(b *testing.B) {
	for _, count := range []int{1, 4, 8} {
		b.Run(fmt.Sprintf("%d-users", count), func(b *testing.B) {
			c := receiveChain{}
			defer c.close()
			processing := ProcessingConfig{Postprocess: [1]ProcessorSpec{{Name: "agc", Backend: "speex"}}}
			processors := make([]*receiveProcessor, count)
			for i := range processors {
				var err error
				processors[i], err = c.peer(uint16(i), "peer", processing, time.Now())
				if err != nil {
					b.Fatal(err)
				}
			}
			frame := make([]float32, FrameSamples)
			source := make([]float32, FrameSamples)
			receiveTone(source, .02)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for _, p := range processors {
					copy(frame, source)
					p.process(frame, false)
				}
			}
		})
	}
}
