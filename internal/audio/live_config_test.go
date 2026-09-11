package audio

import (
	"context"
	"errors"
	"testing"
)

func TestLiveSettingsPreserveDevicesAndChannelTransitionResetsAudio(t *testing.T) {
	transport := &fakeTransport{codec: CodecOpusVoice}
	devices := &fakeDeviceFactory{}
	e := newWithFactory(transport, nil, devices)
	defer e.Close()
	config := VoiceConfig{Enabled: true, Muted: true, Volume: 100, ActivationMode: "vad", VADThresholdDB: -40}
	if err := e.Configure(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	first := e.run
	for i := range 20 {
		config.InputGain = i * 10
		config.Volume, config.VADThresholdDB, config.Ducking = 80-i, -40+i, i%2 == 0
		if err := e.Configure(context.Background(), config); err != nil {
			t.Fatal(err)
		}
	}
	if e.run != first || len(devices.opens) != 1 || devices.last().session.closed {
		t.Fatal("live settings reopened audio devices")
	}
	if first.currentConfig() != config || e.Status().Config != config {
		t.Fatal("last configuration was not applied")
	}
	if len(transport.mutes) != 1 {
		t.Fatal("live settings sent redundant server commands")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	changed := config
	changed.Volume = 1
	if err := e.Configure(ctx, changed); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled update: %v", err)
	}
	if e.Status().Config != config {
		t.Fatal("canceled configuration applied")
	}
	if err := e.ChannelChanged(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.run == first || len(devices.opens) != 2 || !devices.opens[0].session.closed {
		t.Fatal("channel transition reused old audio queues")
	}
	if len(transport.mutes) != 1 {
		t.Fatal("channel transition resent unchanged mute state")
	}
	config.Muted = false
	if err := e.Configure(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if len(devices.opens) != 3 || !devices.last().capture || len(transport.mutes) != 2 {
		t.Fatal("unmute did not reconfigure capture and server")
	}
}
