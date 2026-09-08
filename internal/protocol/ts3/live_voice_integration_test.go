//go:build integration

package ts3

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/honeybbq/teamspeak-go/commands"
	"github.com/thesyncim/gopus"
	"github.com/tsukiyoz/resona/internal/audio"
	"github.com/tsukiyoz/resona/internal/client"
)

// This probe creates its own temporary channel. No voice is sent before both
// clients have confirmed membership there, and no existing channel is edited.
func TestLiveVoiceRoundTrip(t *testing.T) {
	address, identity := os.Getenv("RESONA_TS_ADDRESS"), os.Getenv("RESONA_TS_IDENTITY")
	if os.Getenv("RESONA_TS_VOICE_TEST") != "1" || address == "" || identity == "" {
		t.Skip("explicit live voice test authorization and identity are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	connect := func(nickname string) *connection {
		t.Helper()
		remote, err := New(identity).Connect(ctx, client.ServerProfile{Address: address, Nickname: nickname}, os.Getenv("RESONA_TS_PASSWORD"), func(client.RemoteState) {})
		if err != nil {
			t.Fatalf("test connection: %v", err)
		}
		c := remote.(*connection)
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	snapshot := func(c *connection) client.RemoteState {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.state.snapshot()
	}
	wait := func(check func() bool) {
		t.Helper()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal("live voice state timeout")
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	a := connect("Resona-Voice-A")
	name := fmt.Sprintf("Resona Test Voice %d", time.Now().UnixNano())
	if err := a.client.ExecCommandContext(ctx, commands.BuildCommand("channelcreate", map[string]string{
		"channel_name": name, "channel_topic": "Resona isolated voice verification",
		"channel_codec": "4", "channel_flag_permanent": "0", "channel_flag_semi_permanent": "0",
	})); err != nil {
		t.Fatalf("create own temporary channel: %v", err)
	}
	var channelID string
	wait(func() bool {
		s := snapshot(a)
		for _, ch := range s.Channels {
			if ch.Name == name && ch.ID == s.ChannelID {
				channelID = ch.ID
				return true
			}
		}
		return false
	})
	b := connect("Resona-Voice-B")
	if err := b.MoveChannel(ctx, channelID); err != nil {
		t.Fatal(err)
	}
	wait(func() bool { return snapshot(b).ChannelID == channelID })
	for _, c := range []*connection{a, b} {
		if err := c.SetVoiceMuted(ctx, false, false); err != nil {
			t.Fatal(err)
		}
	}
	probe := func(sender, receiver *connection) {
		t.Helper()
		packets := make(chan audio.Packet, 128)
		receiver.SetVoiceHandler(func(p audio.Packet) {
			select {
			case packets <- p:
			default:
			}
		})
		defer receiver.SetVoiceHandler(nil)
		encoder, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 1, Application: gopus.ApplicationVoIP})
		if err != nil {
			t.Fatal(err)
		}
		pcm, data := make([]float32, 960), make([]byte, 1275)
		for frame := 0; frame < 30; frame++ {
			if snapshot(sender).ChannelID != channelID || snapshot(receiver).ChannelID != channelID {
				t.Fatal("left own test channel; refusing voice")
			}
			for i := range pcm {
				pcm[i] = float32(0.02 * math.Sin(2*math.Pi*440*float64(frame*960+i)/48000))
			}
			n, err := encoder.Encode(pcm, data)
			if err != nil {
				t.Fatal(err)
			}
			if err := sender.SendVoice(data[:n], audio.CodecOpusVoice); err != nil {
				t.Fatal(err)
			}
			time.Sleep(20 * time.Millisecond)
		}
		decoder, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
		if err != nil {
			t.Fatal(err)
		}
		expectedID, _ := strconv.Atoi(snapshot(sender).SelfID)
		decoded, nonzero := 0, false
		deadline := time.NewTimer(3 * time.Second)
		defer deadline.Stop()
		for decoded < 10 {
			select {
			case p := <-packets:
				if p.SenderID != uint16(expectedID) || p.Codec != audio.CodecOpusVoice {
					t.Fatal("unexpected voice sender or codec")
				}
				out := make([]float32, 5760)
				n, err := decoder.Decode(p.Data, out)
				if err != nil || n != 960 {
					t.Fatalf("decode: frames=%d error=%v", n, err)
				}
				for _, sample := range out[:n] {
					nonzero = nonzero || math.Abs(float64(sample)) > 0.00001
				}
				decoded++
			case <-deadline.C:
				t.Fatalf("received only %d decoded voice frames", decoded)
			}
		}
		if !nonzero {
			t.Fatal("decoded audio is silent")
		}
		t.Logf("decoded %d real server-forwarded Opus frames", decoded)
	}
	probe(a, b)
	probe(b, a)
	_ = b.Close()
	_ = a.Close()
	verify := connect("Resona-Voice-Cleanup")
	wait(func() bool {
		for _, ch := range snapshot(verify).Channels {
			if ch.ID == channelID || ch.Name == name {
				return false
			}
		}
		return true
	})
	t.Log("bidirectional voice passed; own temporary channel removed")
}
