package audio

import (
	"context"
	"testing"
	"time"
)

func TestConnectedMonitorMixesLocallyWithoutSending(t *testing.T) {
	transport := &fakeTransport{codec: CodecOpusVoice}
	devices := &fakeDeviceFactory{}
	engine := newWithFactory(transport, nil, devices)
	defer engine.Close()
	config := VoiceConfig{Enabled: true, Muted: true, LocalMonitor: true, Volume: 50, ActivationMode: "ptt"}
	if err := engine.Configure(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	input := make([]float32, FrameSamples)
	for i := range input {
		input[i] = .2
	}
	call := devices.last()
	if !call.capture || !call.playback {
		t.Fatal("test needs duplex device")
	}
	call.callbacks.capture(input)
	deadline := time.Now().Add(time.Second)
	for engine.run.monitorPCM.Available() < FrameSamples*2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	output := make([]float32, FrameSamples*2)
	call.callbacks.playback(output)
	if !pcmHasActivity(output) {
		t.Fatal("local microphone loopback is silent")
	}
	packet := encodedTonePacket(t, CodecOpusVoice)
	for i := range 3 {
		transport.emit(Packet{SenderID: 8, Codec: CodecOpusVoice, Sequence: uint16(i), Data: packet})
	}
	deadline = time.Now().Add(time.Second)
	for engine.run.playbackPCM.Available() < FrameSamples*2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if engine.run.playbackPCM.Available() == 0 {
		t.Fatal("monitor interrupted remote playback")
	}
	call.callbacks.playback(output)
	if !pcmHasActivity(output) {
		t.Fatal("local/remote mix is silent")
	}
	transport.mu.Lock()
	sends := len(transport.sends)
	muted := transport.mutes[len(transport.mutes)-1]
	transport.mu.Unlock()
	if sends != 0 || muted != [2]bool{true, false} {
		t.Fatal("test transmitted or muted channel playback")
	}
	if engine.Status().LocalSpeaking {
		t.Fatal("local test marked network speaking")
	}
	engine.SuspendCapture()
	call.callbacks.capture(input)
	config.LocalMonitor = false
	if err := engine.Configure(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if devices.last().capture {
		t.Fatal("muted user kept capture after test")
	}
}
