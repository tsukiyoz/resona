package audio

import (
	"context"
	"math"
	"sync/atomic"
	"testing"

	"github.com/thesyncim/gopus"
)

type bitrateTransport struct {
	fakeTransport
	rate atomic.Int32
}

func (t *bitrateTransport) VoiceBitrate() int { return int(t.rate.Load()) }

func TestChannelBitrateHotUpdatePreservesDevicesAndPTT(t *testing.T) {
	transport := &bitrateTransport{fakeTransport: fakeTransport{codec: CodecOpusVoice}}
	transport.rate.Store(20000)
	devices := &fakeDeviceFactory{}
	e := newWithFactory(transport, nil, devices)
	defer e.Close()
	config := VoiceConfig{Enabled: true, Volume: 100, InputGain: 100, ActivationMode: "ptt"}
	if err := e.Configure(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	first := e.run
	if err := e.SetPushToTalk(true); err != nil {
		t.Fatal(err)
	}
	input := make([]float32, FrameSamples)
	for i := range input {
		input[i] = .2 * float32(math.Sin(2*math.Pi*440*float64(i)/SampleRate))
	}
	for i, bitrate := range []int32{20000, 64000, 16000, 48000, 32000} {
		transport.rate.Store(bitrate)
		devices.last().callbacks.capture(input)
		waitUntil(t, func() bool { transport.mu.Lock(); defer transport.mu.Unlock(); return len(transport.sends) == i+1 })
		if !e.Status().PushToTalkPressed || e.run != first || len(devices.opens) != 1 {
			t.Fatal("bitrate reset device or PTT")
		}
	}
	// Stop joins the encoder goroutine before reading its private codec state.
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if first.encoder.Bitrate() != 32000 {
		t.Fatalf("encoder target=%d", first.encoder.Bitrate())
	}
	decoder, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(SampleRate, 1))
	if err != nil {
		t.Fatal(err)
	}
	for i, packet := range transport.sends {
		if len(packet) == 0 {
			continue
		}
		if n, err := decoder.Decode(packet, make([]float32, 5760)); err != nil || n != FrameSamples {
			t.Fatalf("frame %d after rate switch: samples=%d err=%v", i, n, err)
		}
	}
	if len(transport.mutes) != 2 {
		t.Fatalf("unexpected mute commands: %v", transport.mutes)
	}
}

func TestChannelBitrateDoesNotEnableMutedMonitorTransmission(t *testing.T) {
	transport := &bitrateTransport{fakeTransport: fakeTransport{codec: CodecOpusVoice}}
	transport.rate.Store(32000)
	devices := &fakeDeviceFactory{}
	e := newWithFactory(transport, nil, devices)
	defer e.Close()
	config := VoiceConfig{Enabled: true, Muted: true, LocalMonitor: true, Volume: 100, InputGain: 100, ActivationMode: "ptt"}
	if err := e.Configure(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	input := make([]float32, FrameSamples)
	for i := range input {
		input[i] = .1
	}
	for _, bitrate := range []int32{16000, 64000, 20000} {
		transport.rate.Store(bitrate)
		devices.last().callbacks.capture(input)
		waitUntil(t, func() bool { return e.run.monitorPCM.Available() >= FrameSamples*2 })
		devices.last().callbacks.playback(make([]float32, FrameSamples*2))
	}
	_ = e.Close()
	if len(transport.sends) != 0 || len(devices.opens) != 1 || e.Status().PushToTalkPressed {
		t.Fatal("quality update leaked monitor audio")
	}
}

func TestChannelBitrateValidationAndLegacyDefaults(t *testing.T) {
	for _, bitrate := range []int32{0, 15999, 64001} {
		transport := &bitrateTransport{fakeTransport: fakeTransport{codec: CodecOpusVoice}}
		transport.rate.Store(bitrate)
		e := newWithFactory(transport, nil, &fakeDeviceFactory{})
		if err := e.Configure(context.Background(), VoiceConfig{Enabled: true, Muted: true, Volume: 100}); err == nil {
			t.Fatal("invalid bitrate accepted")
		}
		_ = e.Close()
	}
	for codec, want := range map[Codec]int{CodecOpusVoice: 48000, CodecOpusMusic: 96000} {
		e := newWithFactory(&fakeTransport{codec: codec}, nil, &fakeDeviceFactory{})
		r, err := newEngineRun(e, codec, 100)
		if err != nil {
			t.Fatal(err)
		}
		if r.encoder.Bitrate() != want {
			t.Fatal("legacy codec default changed")
		}
		r.cancel()
		_ = e.Close()
	}
}
