//go:build cgo

package main

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesyncim/gopus"
	"github.com/tsukiyoz/resona/internal/audio"
	"github.com/tsukiyoz/resona/internal/client"
	"github.com/tsukiyoz/resona/internal/protocol/native"
)

// Explicit local listening diagnostic, never connects by default.
func TestLocalListeningCapture(t *testing.T) {
	address := os.Getenv("RESONA_LISTENING_ADDRESS")
	if address == "" {
		t.Skip("explicit loopback address required")
	}
	if len(address) < 10 || address[:10] != "127.0.0.1:" {
		t.Fatal("loopback only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	c, err := (native.Connector{}).Connect(ctx, client.ServerProfile{Protocol: "resona-noise", Address: address, ServerPublicKey: os.Getenv("RESONA_LISTENING_KEY"), Nickname: "本地音源诊断"}, "", func(client.RemoteState) {})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.MoveChannel(ctx, "4"); err != nil {
		t.Fatal(err)
	}
	packets := make(chan audio.Packet, 128)
	c.(audio.Transport).SetVoiceHandler(func(p audio.Packet) {
		select {
		case packets <- p:
		default:
		}
	})
	decoders := map[uint16]*gopus.Decoder{}
	samples := map[uint16][]float32{}
	last := map[uint16]audio.Packet{}
	counts, gaps := 0, 0
	var maxGap time.Duration
	for {
		select {
		case <-ctx.Done():
			for id, pcm := range samples {
				path := filepath.Join("../../build/listening", "received-"+string(rune('0'+id))+".f32")
				b := make([]byte, len(pcm)*4)
				for i, x := range pcm {
					binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(x))
				}
				if err := os.WriteFile(path, b, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Logf("packets=%d missing_sequences=%d max_contiguous_arrival_gap=%s speakers=%d", counts, gaps, maxGap, len(samples))
			if counts == 0 {
				t.Fatal("no audio")
			}
			return
		case p := <-packets:
			if prev, ok := last[p.SenderID]; ok {
				if p.Sequence != prev.Sequence+1 {
					gaps++
				}
				gap := p.ReceivedAt.Sub(prev.ReceivedAt)
				if gap < time.Second {
					maxGap = max(maxGap, gap)
				}
			}
			last[p.SenderID] = p
			if p.End {
				delete(decoders, p.SenderID)
				continue
			}
			d := decoders[p.SenderID]
			if d == nil {
				d, err = gopus.NewDecoder(gopus.DefaultDecoderConfig(audio.SampleRate, 1))
				if err != nil {
					t.Fatal(err)
				}
				decoders[p.SenderID] = d
			}
			out := make([]float32, audio.FrameSamples)
			n, err := d.Decode(p.Data, out)
			if err != nil {
				t.Fatal(err)
			}
			samples[p.SenderID] = append(samples[p.SenderID], out[:n]...)
			counts++
		}
	}
}
