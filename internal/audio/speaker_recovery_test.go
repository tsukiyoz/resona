package audio

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/thesyncim/gopus"
)

func TestSpeakerRecoversAfterPartialFrameAndSequenceRestart(t *testing.T) {
	transport := &fakeTransport{codec: CodecOpusVoice}
	devices := &fakeDeviceFactory{}
	engine := newWithFactory(transport, nil, devices)
	defer engine.Close()
	if err := engine.Configure(context.Background(), VoiceConfig{Enabled: true, Muted: true, Volume: 100}); err != nil {
		t.Fatal(err)
	}
	encoder, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: SampleRate, Channels: 1, Application: gopus.ApplicationVoIP})
	if err != nil {
		t.Fatal(err)
	}
	if err = encoder.SetFrameSize(FrameSamples / 2); err != nil {
		t.Fatal(err)
	}
	// Three valid 10ms packets leave half a 20ms output frame. A missing next
	// packet must not pin this speaker's old sequence space indefinitely.
	pcm := make([]float32, FrameSamples/2)
	for i := range pcm {
		pcm[i] = float32(math.Sin(float64(i)*.08) * .2)
	}
	buffer := make([]byte, MaxOpusPacketSize)
	when := time.Now().Add(-200 * time.Millisecond)
	for i := range 3 {
		n, err := encoder.Encode(pcm, buffer)
		if err != nil {
			t.Fatal(err)
		}
		transport.emit(Packet{SenderID: 7, Codec: CodecOpusVoice, Sequence: uint16(1000 + i), Data: buffer[:n], ReceivedAt: when})
	}
	time.Sleep(450 * time.Millisecond)
	output := make([]float32, FrameSamples*2*pcmBufferFrames)
	devices.last().callbacks.playback(output)
	packet := encodedTonePacket(t, CodecOpusVoice)
	for _, sender := range []uint16{7, 8, 9} {
		for i := range 3 {
			transport.emit(Packet{SenderID: sender, Codec: CodecOpusVoice, Sequence: uint16(i), Data: packet})
		}
	}
	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(engine.Status().SpeakingClientIDs) == 3 && engine.run.playbackPCM.Available() > 0 {
			devices.last().callbacks.playback(output)
			if !pcmHasActivity(output) {
				t.Fatal("speaker activity has no audible PCM")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("valid restarted speaker remained inaudible after idle timeout")
}
