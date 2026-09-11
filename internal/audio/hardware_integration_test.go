//go:build cgo && (darwin || windows)

package audio

import (
	"context"
	"math"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestHardwareAudioLifecycle(t *testing.T) {
	if os.Getenv("RESONA_AUDIO_HARDWARE") != "1" {
		t.Skip("set RESONA_AUDIO_HARDWARE=1 to open real audio devices")
	}
	devices, err := Devices()
	if err != nil {
		t.Fatal(err)
	}
	var defaultInputID, defaultOutputID string
	for _, device := range devices {
		if device.Kind == DeviceInput && device.Default {
			defaultInputID = device.ID
		}
		if device.Kind == DeviceOutput && device.Default {
			defaultOutputID = device.ID
		}
	}
	if defaultInputID == "" || defaultOutputID == "" {
		t.Fatalf("missing defaults: input=%q output=%q devices=%+v", defaultInputID, defaultOutputID, devices)
	}

	factory := nativeDeviceFactory{}
	var outputFrames atomic.Uint64
	phase := 0
	output, err := factory.Open(VoiceConfig{OutputDeviceID: defaultOutputID}, true, false, deviceCallbacks{playback: func(samples []float32) {
		for frame := 0; frame < len(samples)/2; frame++ {
			sample := float32(math.Sin(2*math.Pi*220*float64(phase)/SampleRate)) * 0.015
			samples[frame*2], samples[frame*2+1] = sample, sample
			phase++
		}
		outputFrames.Add(uint64(len(samples) / 2))
	}})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if outputFrames.Load() == 0 {
		t.Fatal("output callback received no frames")
	}

	var captureFrames atomic.Uint64
	input, err := factory.Open(VoiceConfig{InputDeviceID: defaultInputID}, false, true, deviceCallbacks{capture: func(samples []float32) {
		captureFrames.Add(uint64(len(samples)))
	}})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if captureFrames.Load() == 0 {
		t.Fatal("capture callback received no frames")
	}
	closedCount := captureFrames.Load()
	time.Sleep(100 * time.Millisecond)
	if captureFrames.Load() != closedCount {
		t.Fatal("capture callback continued after Close")
	}
}

func TestHardwareEngineMuteUnmuteDeafen(t *testing.T) {
	if os.Getenv("RESONA_AUDIO_HARDWARE") != "1" {
		t.Skip("set RESONA_AUDIO_HARDWARE=1 to open the real microphone")
	}
	transport := &fakeTransport{codec: CodecOpusVoice}
	engine := New(transport, nil)
	defer func() { _ = engine.Close() }()
	config := VoiceConfig{Enabled: true, Muted: true, Volume: 5, InputGain: 100}
	if err := engine.Configure(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	transport.mu.Lock()
	mutedPackets := len(transport.sends)
	transport.mu.Unlock()
	if mutedPackets != 0 {
		t.Fatalf("muted engine sent %d packets", mutedPackets)
	}

	config.Muted = false
	if err := engine.Configure(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool {
		transport.mu.Lock()
		defer transport.mu.Unlock()
		return len(transport.sends) > 0
	})
	config.Deafened = true
	if err := engine.Configure(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	transport.mu.Lock()
	afterDeafen := len(transport.sends)
	transport.mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.sends) != afterDeafen {
		t.Fatalf("deafened engine continued sending: %d -> %d", afterDeafen, len(transport.sends))
	}
}
