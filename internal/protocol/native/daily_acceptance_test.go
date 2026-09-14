package native

import (
	"context"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/thesyncim/gopus"
	"github.com/tsukiyoz/resona/internal/audio"
	"github.com/tsukiyoz/resona/internal/client"
	w "github.com/tsukiyoz/resona/internal/nativewire"
	"github.com/tsukiyoz/resona/internal/noiseudp"
	"github.com/tsukiyoz/resona/internal/server"
)

func TestDailyFourSpeakersReachEveryOtherMember(t *testing.T) {
	t.Run("noise", func(t *testing.T) {
		profile, stop, done := startServer(t)
		defer func() { stop(); <-done }()
		connections := make([]*connection, 4)
		observers := make([]*observer, 4)
		packets := make([]chan audio.Packet, 4)
		for i := range connections {
			profile.Nickname = fmt.Sprintf("speaker-%d", i)
			connections[i], observers[i] = connectTest(t, profile)
			packets[i] = make(chan audio.Packet, 128)
			out := packets[i]
			connections[i].SetVoiceHandler(func(p audio.Packet) {
				select {
				case out <- p:
				default:
				}
			})
			if err := connections[i].SetVoiceMuted(context.Background(), false, false); err != nil {
				t.Fatal(err)
			}
		}
		eventually(t, func() bool {
			for _, o := range observers {
				if len(o.snapshot().Users) != 4 {
					return false
				}
			}
			return true
		})
		encoder, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 1, Application: gopus.ApplicationVoIP})
		if err != nil {
			t.Fatal(err)
		}
		if err = encoder.SetBitrate(32000); err != nil {
			t.Fatal(err)
		}
		pcm := make([]float32, 960)
		for i := range pcm {
			pcm[i] = float32(i%48) / 240
		}
		encoded := make([]byte, 1024)
		n, err := encoder.Encode(pcm, encoded)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for _, c := range connections {
			wg.Go(func() {
				ticker := time.NewTicker(20 * time.Millisecond)
				defer ticker.Stop()
				for range 12 {
					<-ticker.C
					if err := c.SendVoice(encoded[:n], audio.CodecOpusVoice); err != nil {
						t.Error(err)
						return
					}
				}
			})
		}
		wg.Wait()
		for i, received := range packets {
			seen := map[uint16]bool{}
			deadline := time.NewTimer(2 * time.Second)
			for len(seen) < 3 {
				select {
				case packet := <-received:
					if id(packet.SenderID) == observers[i].snapshot().SelfID {
						t.Fatal("server echoed self")
					}
					if !seen[packet.SenderID] {
						decoder, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
						if err != nil {
							t.Fatal(err)
						}
						if samples, err := decoder.Decode(packet.Data, make([]float32, 5760)); err != nil || samples != 960 {
							t.Fatalf("speaker decode: %d %v", samples, err)
						}
					}
					seen[packet.SenderID] = true
				case <-deadline.C:
					t.Fatalf("listener %d received only %d of 3 speakers", i, len(seen))
				}
			}
			deadline.Stop()
		}
		// Voice-state changes must reach the workspace observer without detail queries.
		peerID := observers[0].snapshot().SelfID
		for _, muted := range []bool{true, false} {
			if err := connections[0].SetVoiceMuted(context.Background(), muted, muted); err != nil {
				t.Fatal(err)
			}
			eventually(t, func() bool {
				for _, o := range observers {
					found := false
					for _, user := range o.snapshot().Users {
						if user.ID == peerID {
							found = user.VoiceStateKnown && user.InputMuted == muted && user.OutputMuted == muted
						}
					}
					if !found {
						return false
					}
				}
				return true
			})
		}
	})
}

func TestDailyNoiseServerRestartAndExplicitReconnect(t *testing.T) {
	key, err := noiseudp.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := noiseudp.PublicKey(key)
	cfg := server.Config{Name: "restart acceptance", NoiseKey: key, Channels: []w.Channel{{ID: 1, Name: "Lobby"}, {ID: 2, Name: "Game", Bitrate: 32000}}}
	start := func(address string) (*server.Server, context.CancelFunc, <-chan error) {
		s, err := server.Listen(address, cfg)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- s.Serve(ctx) }()
		t.Cleanup(cancel)
		return s, cancel, done
	}
	s, stop, done := start("127.0.0.1:0")
	profile := client.ServerProfile{Protocol: "resona-noise", Address: s.Addr().String(), ServerPublicKey: hex.EncodeToString(pub), Nickname: "persistent member"}
	connector := Connector{IdentityPath: filepath.Join(t.TempDir(), "native.key")}
	connect := func() (*connection, *observer) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		o := &observer{}
		c, err := connector.Connect(ctx, profile, "", o.update)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c.(*connection), o
	}
	a, oa := connect()
	identity := oa.snapshot().IdentityUID
	if err = a.MoveChannel(context.Background(), "2"); err != nil {
		t.Fatal(err)
	}
	details, err := a.ReadChannelDetails(context.Background(), "2")
	if err != nil || details.Default == nil || *details.Default || details.Permanent != nil || details.CodecQuality != nil {
		t.Fatalf("native details: %+v %v", details, err)
	}
	stop()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return oa.snapshot().Closed })
	if err = a.SendVoice([]byte{1}, audio.CodecOpusVoice); err == nil {
		t.Fatal("dead connection still accepted voice")
	}
	_, stop, done = start(profile.Address)
	defer func() { stop(); <-done }()
	b, ob := connect()
	if ob.snapshot().IdentityUID != identity || ob.snapshot().ChannelID != "1" {
		t.Fatal("reconnect lost identity or retained stale location")
	}
	if err = b.MoveChannel(context.Background(), "2"); err != nil {
		t.Fatal(err)
	}
	if b.VoiceBitrate() != 32000 {
		t.Fatal("reconnect failed to restore authoritative channel bitrate")
	}
	if err = b.Close(); err != nil {
		t.Fatal(err)
	}
}
