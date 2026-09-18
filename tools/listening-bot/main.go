//go:build cgo

// listening-bot hosts an isolated loopback-only listening comparison.
package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/thesyncim/gopus"
	"github.com/tsukiyoz/resona/internal/audio"
	"github.com/tsukiyoz/resona/internal/audio/speexdsp"
	"github.com/tsukiyoz/resona/internal/audio/webrtc"
	"github.com/tsukiyoz/resona/internal/client"
	w "github.com/tsukiyoz/resona/internal/nativewire"
	"github.com/tsukiyoz/resona/internal/noiseudp"
	"github.com/tsukiyoz/resona/internal/protocol/native"
	"github.com/tsukiyoz/resona/internal/server"
)

func readPCM(path string) ([]float32, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	const limit = audio.SampleRate * 120 * 4
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if len(b) < audio.FrameSamples*4 || len(b) > limit || len(b)%4 != 0 {
		return nil, errors.New("input must be 48kHz mono float32 little-endian PCM, 20ms to 120s")
	}
	pcm := make([]float32, ((len(b)/4+audio.FrameSamples-1)/audio.FrameSamples)*audio.FrameSamples)
	for i := 0; i < len(b)/4; i++ {
		v := math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) || v < -1 || v > 1 {
			return nil, errors.New("PCM must be finite and within [-1,1]")
		}
		pcm[i] = v
	}
	return pcm, nil
}

func encode(pcm []float32, mode string) ([][]byte, error) {
	process := func([]float32) error { return nil }
	switch mode {
	case "speex":
		p, err := speexdsp.NewANS(audio.FrameSamples, audio.SampleRate, 2)
		if err != nil {
			return nil, err
		}
		defer p.Close()
		process = func(f []float32) error { p.Process(f, nil); return nil }
	case "webrtc":
		p, err := webrtc.New(2, 2, 0, 0)
		if err != nil {
			return nil, err
		}
		defer p.Close()
		process = func(f []float32) error {
			if code := p.Process(f, nil); code != 0 {
				return fmt.Errorf("WebRTC process: %d", code)
			}
			return nil
		}
	case "raw":
	default:
		return nil, fmt.Errorf("unknown mode %q", mode)
	}
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: audio.SampleRate, Channels: 1, Application: gopus.ApplicationVoIP})
	if err != nil {
		return nil, err
	}
	if err = enc.SetBitrate(32000); err != nil {
		return nil, err
	}
	if err = enc.SetComplexity(5); err != nil {
		return nil, err
	}
	frame, packet := make([]float32, audio.FrameSamples), make([]byte, audio.MaxOpusPacketSize)
	packets := make([][]byte, 0, len(pcm)/audio.FrameSamples)
	// Warm up with a whole loop; retain only the second pass for steady-state listening.
	for pass := 0; pass < 2; pass++ {
		for start := 0; start < len(pcm); start += audio.FrameSamples {
			copy(frame, pcm[start:start+audio.FrameSamples])
			if err := process(frame); err != nil {
				return nil, err
			}
			n, err := enc.Encode(frame, packet)
			if err != nil {
				return nil, err
			}
			if pass == 1 {
				packets = append(packets, append([]byte(nil), packet[:n]...))
			}
		}
	}
	return packets, nil
}

func agcLevel(pcm []float32, db float64) ([]float32, error) {
	var energy float64
	for _, x := range pcm {
		energy += float64(x) * float64(x)
	}
	if len(pcm) == 0 || energy == 0 {
		return nil, errors.New("AGC source must contain speech")
	}
	gain := math.Pow(10, db/20) / math.Sqrt(energy/float64(len(pcm)))
	result := make([]float32, len(pcm))
	for i, x := range pcm {
		result[i] = float32(float64(x) * gain)
		if math.Abs(float64(result[i])) > .95 {
			return nil, errors.New("AGC source would clip at target RMS")
		}
	}
	return result, nil
}

func serve(ctx context.Context, pcm, agc []float32, ready func(client.ServerProfile)) error {
	modes := []string{"raw", "speex", "webrtc"}
	names := []string{"01 原始人声与底噪", "02 SpeexDSP ANS 中等", "03 WebRTC NS 中等"}
	packets := make([][][]byte, len(modes))
	channels := make([]w.Channel, len(modes))
	for i, mode := range modes {
		var err error
		packets[i], err = encode(pcm, mode)
		if err != nil {
			return fmt.Errorf("prepare %s: %w", mode, err)
		}
		channels[i] = w.Channel{ID: uint16(i + 1), Name: names[i], Bitrate: 32000}
	}
	if len(agc) == 0 || len(agc) > audio.SampleRate*10 {
		return errors.New("one clean AGC speech input of at most ten seconds required")
	}
	channels = append(channels, w.Channel{ID: 4, Name: "04 AGC 三人轮流（轻声 / 正常 / 较响）", Bitrate: 32000})
	agcPackets := make([][][]byte, 3)
	for i := range agcPackets {
		leveled, err := agcLevel(agc, -34+float64(i)*8)
		if err != nil {
			return err
		}
		agcPackets[i], err = encode(leveled, "raw")
		if err != nil {
			return err
		}
	}
	key, err := noiseudp.GenerateKey()
	if err != nil {
		return err
	}
	s, err := server.Listen("127.0.0.1:0", server.Config{Name: "Resona 本地音质试听", NoiseKey: key, Channels: channels, MaxClients: 8})
	if err != nil {
		return err
	}
	serverCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- s.Serve(serverCtx) }()
	defer func() { cancel(); <-done }()
	pub, _ := noiseudp.PublicKey(key)
	profile := client.ServerProfile{Protocol: "resona-noise", Address: s.Addr().String(), ServerPublicKey: hex.EncodeToString(pub)}
	transports := make([]audio.Transport, 0, len(modes))
	botNames := []string{"raw", "speex", "webrtc", "轻声 -34 dBFS", "正常 -26 dBFS", "较响 -18 dBFS"}
	for i, mode := range botNames {
		profile.Nickname = "试听机器人 / " + mode
		setup, stop := context.WithTimeout(ctx, 8*time.Second)
		c, err := (native.Connector{}).Connect(setup, profile, "", func(client.RemoteState) {})
		if err != nil {
			stop()
			return err
		}
		defer c.Close()
		if err = c.MoveChannel(setup, strconv.Itoa(min(i+1, 4))); err != nil {
			stop()
			return err
		}
		voice := c.(audio.Transport)
		err = voice.SetVoiceMuted(setup, false, true)
		stop()
		if err != nil {
			return err
		}
		transports = append(transports, voice)
	}
	profile.Nickname = "试听用户"
	ready(profile)
	// Fixed, synchronized 20ms pacing; no catch-up bursts after scheduler stalls.
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	index := 0
	agcSpeaker, agcFrame := 0, 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			for i, voice := range transports {
				if i >= 3 {
					continue
				}
				if err := voice.SendVoice(packets[i][index], audio.CodecOpusVoice); err != nil {
					return err
				}
			}
			if agcFrame < len(agcPackets[agcSpeaker]) {
				if err := transports[3+agcSpeaker].SendVoice(agcPackets[agcSpeaker][agcFrame], audio.CodecOpusVoice); err != nil {
					return err
				}
			} else if agcFrame == len(agcPackets[agcSpeaker]) {
				if err := transports[3+agcSpeaker].SendVoice(nil, audio.CodecOpusVoice); err != nil {
					return err
				}
			}
			agcFrame++
			if agcFrame >= len(agcPackets[agcSpeaker])+25 {
				agcSpeaker, agcFrame = (agcSpeaker+1)%3, 0
			}
			index = (index + 1) % len(packets[0])
		}
	}
}

func main() {
	path := flag.String("pcm", "build/listening/noisy.f32", "48kHz mono float32 little-endian input")
	agcPath := flag.String("agc-pcm", "build/listening/agc.f32", "one clean 48kHz mono f32le phrase, reused at all three levels")
	duration := flag.Duration("duration", 2*time.Hour, "automatic stop after this duration")
	flag.Parse()
	if *duration <= 0 {
		fmt.Fprintln(os.Stderr, "duration must be positive")
		os.Exit(1)
	}
	pcm, err := readPCM(*path)
	var agc []float32
	if err == nil {
		agc, err = readPCM(*agcPath)
	}
	if err == nil {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		ctx, cancel := context.WithTimeout(ctx, *duration)
		defer cancel()
		err = serve(ctx, pcm, agc, func(p client.ServerProfile) {
			fmt.Printf("Address: %s\nPublic key: %s\nPassword: none\nLoop: %.1fs; stop after %s or Ctrl-C\n", p.Address, p.ServerPublicKey, float64(len(pcm))/audio.SampleRate, *duration)
		})
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
