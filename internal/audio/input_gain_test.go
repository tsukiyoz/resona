package audio

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/thesyncim/gopus"
)

func TestInputGainScalesAndLimits(t *testing.T) {
	for _, gain := range []int{0, 100, 200} {
		samples := []float32{-.8, -.2, 0, .2, .8}
		applyInputGain(samples, gain)
		for i, original := range []float32{-.8, -.2, 0, .2, .8} {
			want := max(float32(-1), min(float32(1), original*float32(gain)/100))
			if math.Abs(float64(samples[i]-want)) > .00001 {
				t.Fatalf("gain %d sample %d: %f != %f", gain, i, samples[i], want)
			}
		}
	}
}

func TestInputGainHotUpdatesBothMonitorPaths(t *testing.T) {
	for _, online := range []bool{false, true} {
		t.Run(map[bool]string{false: "offline", true: "online"}[online], func(t *testing.T) {
			transport := &fakeTransport{codec: CodecOpusVoice}
			devices := &fakeDeviceFactory{}
			e := newWithFactory(transport, nil, devices)
			e.monitor = !online
			defer e.Close()
			config := VoiceConfig{Enabled: true, Muted: online, LocalMonitor: online, Volume: 50, InputGain: 100, ActivationMode: "ptt"}
			if err := e.Configure(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			run := e.run
			for _, gain := range []int{0, 200, 50, 100} {
				if err := e.SetInputGain(gain); err != nil {
					t.Fatal(err)
				}
				input := make([]float32, FrameSamples)
				for i := range input {
					input[i] = .2
				}
				devices.last().callbacks.capture(input)
				queue := run.playbackPCM
				if online {
					queue = run.monitorPCM
				}
				deadline := time.Now().Add(time.Second)
				for queue.Available() < FrameSamples*2 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if queue.Available() < FrameSamples*2 {
					t.Fatal("monitor frame missing")
				}
				output := make([]float32, FrameSamples*2)
				devices.last().callbacks.playback(output)
				for _, sample := range output {
					if math.Abs(float64(sample-.1*float32(gain)/100)) > .00001 {
						t.Fatalf("gain %d monitor sample %f", gain, sample)
					}
				}
			}
			transport.mu.Lock()
			sends := len(transport.sends)
			transport.mu.Unlock()
			if e.run != run || len(devices.opens) != 1 || sends != 0 || e.Status().PushToTalkPressed {
				t.Fatal("gain changed device or transmission lifecycle")
			}
			before := e.Status().Config
			if err := e.SetInputGain(201); err == nil || e.Status().Config != before {
				t.Fatal("invalid gain changed config")
			}
		})
	}
}

func TestInputGainRejectsBusyEngineWithoutWaiting(t *testing.T) {
	e := newWithFactory(&fakeTransport{codec: CodecOpusVoice}, nil, &fakeDeviceFactory{})
	defer e.Close()
	if err := e.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := e.SetInputGain(200)
	e.release()
	if err == nil {
		t.Fatal("accepted concurrent device transition")
	}
}

func TestInputGainReachesEncodedVoice(t *testing.T) {
	energy := make(map[int]float64)
	for _, gain := range []int{0, 100, 200} {
		transport := &fakeTransport{codec: CodecOpusVoice}
		devices := &fakeDeviceFactory{}
		e := newWithFactory(transport, nil, devices)
		config := VoiceConfig{Enabled: true, Volume: 100, InputGain: 100, ActivationMode: "ptt"}
		if err := e.Configure(context.Background(), config); err != nil {
			t.Fatal(err)
		}
		if err := e.SetPushToTalk(true); err != nil {
			t.Fatal(err)
		}
		if err := e.SetInputGain(gain); err != nil {
			t.Fatal(err)
		}
		if !e.Status().PushToTalkPressed || len(devices.opens) != 1 {
			t.Fatal("gain reset PTT or devices")
		}
		for frame := 0; frame < 4; frame++ {
			input := make([]float32, FrameSamples)
			for i := range input {
				input[i] = .05 * float32(math.Sin(2*math.Pi*440*float64(frame*FrameSamples+i)/SampleRate))
			}
			devices.last().callbacks.capture(input)
			waitUntil(t, func() bool { transport.mu.Lock(); defer transport.mu.Unlock(); return len(transport.sends) == frame+1 })
		}
		decoder, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(SampleRate, 1))
		if err != nil {
			t.Fatal(err)
		}
		transport.mu.Lock()
		packets := append([][]byte(nil), transport.sends...)
		transport.mu.Unlock()
		for _, packet := range packets {
			pcm := make([]float32, 5760)
			n, err := decoder.Decode(packet, pcm)
			if err != nil {
				t.Fatal(err)
			}
			for _, sample := range pcm[:n] {
				energy[gain] += float64(sample * sample)
			}
		}
		if err := e.SetPushToTalk(false); err != nil {
			t.Fatal(err)
		}
		e.Close()
	}
	if energy[0] > .000001 || energy[100] <= 0 {
		t.Fatalf("invalid encoded energy: %v", energy)
	}
	ratio := math.Sqrt(energy[200] / energy[100])
	if ratio < 1.7 || ratio > 2.3 {
		t.Fatalf("encoded gain ratio=%f", ratio)
	}
}

func TestInputGainDoesNotOpenVADForQuietInput(t *testing.T) {
	transport := &fakeTransport{codec: CodecOpusVoice}
	devices := &fakeDeviceFactory{}
	e := newWithFactory(transport, nil, devices)
	defer e.Close()
	config := VoiceConfig{Enabled: true, Volume: 100, InputGain: 200, ActivationMode: "vad", VADThresholdDB: -20}
	if err := e.Configure(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	input := make([]float32, FrameSamples)
	for i := range input {
		input[i] = .08
	}
	devices.last().callbacks.capture(input)
	waitUntil(t, func() bool { return e.run.capturePCM.Available() == 0 })
	time.Sleep(30 * time.Millisecond)
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.sends) != 0 {
		t.Fatal("gain changed VAD threshold semantics")
	}
}
