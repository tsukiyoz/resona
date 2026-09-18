//go:build cgo

package main

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/thesyncim/gopus"
	"github.com/tsukiyoz/resona/internal/audio"
	"github.com/tsukiyoz/resona/internal/audio/webrtc"
	"github.com/tsukiyoz/resona/internal/client"
	"github.com/tsukiyoz/resona/internal/protocol/native"
)

func TestAGCLevelTargetsAndClipping(t *testing.T) {
	pcm := make([]float32, 960)
	for i := range pcm {
		pcm[i] = .2 * float32(math.Sin(2*math.Pi*float64(i)/48))
	}
	for _, db := range []float64{-34, -26, -18} {
		out, err := agcLevel(pcm, db)
		if err != nil {
			t.Fatal(err)
		}
		var energy float64
		for _, x := range out {
			energy += float64(x) * float64(x)
		}
		got := 10 * math.Log10(energy/float64(len(out)))
		if math.Abs(got-db) > .001 {
			t.Fatalf("RMS=%f want %f", got, db)
		}
	}
	if _, err := agcLevel(make([]float32, 960), -18); err == nil {
		t.Fatal("accepted silence")
	}
	spike := make([]float32, 960)
	spike[0] = 1
	if _, err := agcLevel(spike, -18); err == nil {
		t.Fatal("accepted clipping")
	}
}

func TestListeningServerDeliversDecodableVoiceAndStops(t *testing.T) {
	if !webrtc.Available() {
		t.Skip("WebRTC library required")
	}
	pcm := make([]float32, audio.SampleRate)
	for i := range pcm {
		pcm[i] = .15 * float32(math.Sin(2*math.Pi*220*float64(i)/audio.SampleRate))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ready := make(chan client.ServerProfile, 1)
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, pcm, pcm, func(p client.ServerProfile) { ready <- p })
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("test server did not stop")
		}
	}()
	var p client.ServerProfile
	select {
	case p = <-ready:
	case <-ctx.Done():
		t.Fatal("server startup timed out")
	}
	c, err := (native.Connector{}).Connect(ctx, p, "", func(client.RemoteState) {})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	voice := c.(audio.Transport)
	received := make(chan audio.Packet, 64)
	voice.SetVoiceHandler(func(p audio.Packet) {
		select {
		case received <- p:
		default:
		}
	})
	decoder, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(audio.SampleRate, 1))
	if err != nil {
		t.Fatal(err)
	}
	for _, channel := range []string{"1", "2", "3", "4"} {
		if err := c.MoveChannel(ctx, channel); err != nil {
			t.Fatal(err)
		}
		for len(received) > 0 {
			<-received
		}
		select {
		case packet := <-received:
			out := make([]float32, audio.FrameSamples)
			if n, err := decoder.Decode(packet.Data, out); err != nil || n != audio.FrameSamples {
				t.Fatalf("channel %s: decoded %d: %v", channel, n, err)
			}
		case <-ctx.Done():
			t.Fatalf("no voice in channel %s", channel)
		}
	}
	senders := map[uint16]bool{}
	for len(senders) < 3 {
		select {
		case p := <-received:
			senders[p.SenderID] = true
		case <-ctx.Done():
			t.Fatal("AGC channel did not deliver three distinct speakers")
		}
	}
}
