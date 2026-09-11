package audio

import (
	"context"
	"testing"
	"time"
)

func TestActivationThresholdAndHangover(t *testing.T) {
	r := &engineRun{config: VoiceConfig{ActivationMode: "vad", VADThresholdDB: -40}}
	now := time.Now()
	if r.activationOpen(-50, now) {
		t.Fatal("noise opened gate")
	}
	if !r.activationOpen(-35, now) || !r.activationOpen(-50, now.Add(200*time.Millisecond)) {
		t.Fatal("speech or hangover closed")
	}
	if r.activationOpen(-50, now.Add(251*time.Millisecond)) {
		t.Fatal("hangover did not expire")
	}
}

func TestPushToTalkReleaseDropsPendingCapture(t *testing.T) {
	transport := &fakeTransport{codec: CodecOpusVoice}
	devices := &fakeDeviceFactory{}
	engine := newWithFactory(transport, nil, devices)
	defer engine.Close()
	config := VoiceConfig{Enabled: true, Volume: 100, ActivationMode: "ptt"}
	if err := engine.Configure(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	call := devices.last()
	samples := make([]float32, FrameSamples)
	for i := range samples {
		samples[i] = 0.1
	}
	call.callbacks.capture(samples)
	if engine.run.capturePCM.Available() != 0 {
		t.Fatal("PTT captured before explicit press")
	}
	if err := engine.SetPushToTalk(true); err != nil {
		t.Fatal(err)
	}
	engine.run.sendMu.Lock()
	call.callbacks.capture(samples)
	engine.run.sendMu.Unlock()
	if err := engine.SetPushToTalk(false); err != nil {
		t.Fatal(err)
	}
	transport.mu.Lock()
	sends := len(transport.sends)
	transport.mu.Unlock()
	for i := 0; i < 10; i++ {
		call.callbacks.capture(samples)
	}
	time.Sleep(30 * time.Millisecond)
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.sends) != sends {
		t.Fatal("PTT sent after release returned")
	}
	if engine.Status().PushToTalkPressed || engine.Status().LocalSpeaking {
		t.Fatal("PTT release left activity enabled")
	}
}

func TestLocalMicrophoneMonitorWithoutTransport(t *testing.T) {
	engine := NewMicrophoneTest(nil)
	devices := &fakeDeviceFactory{}
	engine.factory = devices
	defer engine.Close()
	if err := engine.Configure(context.Background(), VoiceConfig{Enabled: true, Volume: 50, InputGain: 100, ActivationMode: "ptt"}); err != nil {
		t.Fatal(err)
	}
	input := make([]float32, FrameSamples)
	for i := range input {
		input[i] = 0.2
	}
	devices.last().callbacks.capture(input)
	deadline := time.Now().Add(time.Second)
	for engine.run.playbackPCM.Available() < FrameSamples*2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	output := make([]float32, FrameSamples*2)
	devices.last().callbacks.playback(output)
	for _, sample := range output {
		if sample != 0.1 {
			t.Fatalf("monitor output %f, want 0.1", sample)
		}
	}
	if engine.Status().InputLevelDB >= 0 {
		t.Fatal("missing microphone input level")
	}
}
