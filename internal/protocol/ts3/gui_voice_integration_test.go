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

// A manually gated peer for native GUI acceptance. Control files contain no
// audio or credentials; input samples are decoded and discarded in memory.
func TestGUISoleDedicatedVoicePeer(t *testing.T) {
	address, identity := os.Getenv("RESONA_TS_ADDRESS"), os.Getenv("RESONA_TS_IDENTITY")
	stopFile, toneFile := os.Getenv("RESONA_GUI_STOP_FILE"), os.Getenv("RESONA_GUI_TONE_FILE")
	if os.Getenv("RESONA_GUI_VOICE_TEST") != "1" || address == "" || identity == "" || stopFile == "" || toneFile == "" {
		t.Skip("explicit GUI fixture environment required")
	}
	maxFrames := 25
	if value := os.Getenv("RESONA_GUI_TONE_FRAMES"); value != "" {
		var err error
		maxFrames, err = strconv.Atoi(value)
		if err != nil || maxFrames < 1 || maxFrames > 500 {
			t.Fatal("GUI tone frames must be between 1 and 500")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	connectCtx, connectCancel := context.WithTimeout(ctx, 30*time.Second)
	defer connectCancel()
	remote, err := New(identity).Connect(connectCtx, client.ServerProfile{Address: address, Nickname: "Resona-GUI-TestPeer"}, os.Getenv("RESONA_TS_PASSWORD"), func(client.RemoteState) {})
	if err != nil {
		t.Fatal(err)
	}
	c := remote.(*connection)
	defer c.Close()
	name := fmt.Sprintf("Resona Test GUI %d", time.Now().UnixNano())
	if err := c.client.ExecCommandContext(connectCtx, commands.BuildCommand("channelcreate", map[string]string{
		"channel_name": name, "channel_codec": "4", "channel_flag_permanent": "0", "channel_flag_semi_permanent": "0",
	})); err != nil {
		t.Fatal(err)
	}
	snapshot := func() client.RemoteState { c.mu.Lock(); defer c.mu.Unlock(); return c.state.snapshot() }
	var channelID string
	for channelID == "" {
		s := snapshot()
		for _, ch := range s.Channels {
			if ch.ID == s.ChannelID && ch.Name == name {
				channelID = ch.ID
			}
		}
		select {
		case <-connectCtx.Done():
			t.Fatal("own temporary channel unavailable")
		case <-time.After(20 * time.Millisecond):
		}
	}
	if err := c.SetVoiceMuted(connectCtx, false, false); err != nil {
		t.Fatal(err)
	}
	t.Logf("GUI fixture ready: channel=%s name=%s; expected peer nickname=Resona-GUI-Verify", channelID, name)
	packets := make(chan audio.Packet, 128)
	c.SetVoiceHandler(func(p audio.Packet) {
		select {
		case packets <- p:
		default:
		}
	})
	defer c.SetVoiceHandler(nil)
	decoder, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 1, Application: gopus.ApplicationVoIP})
	if err != nil {
		t.Fatal(err)
	}
	pcm, encoded, decoded := make([]float32, 960), make([]byte, 1275), make([]float32, 5760)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	received, sent := 0, 0
	for {
		select {
		case <-ctx.Done():
			t.Fatal("GUI fixture timed out")
		case p := <-packets:
			s := snapshot()
			allowed := false
			for _, user := range s.Users {
				id, _ := strconv.Atoi(user.ID)
				allowed = allowed || user.ChannelID == channelID && user.Nickname == "Resona-GUI-Verify" && uint16(id) == p.SenderID
			}
			if !allowed || s.ChannelID != channelID || len(p.Data) == 0 {
				continue
			}
			n, err := decoder.Decode(p.Data, decoded)
			if err != nil || n == 0 {
				t.Fatal("GUI microphone Opus decode failed")
			}
			received++
			if received == 1 || received%100 == 0 {
				t.Logf("GUI microphone decoded frames=%d", received)
			}
		case <-ticker.C:
			if _, err := os.Stat(stopFile); err == nil {
				t.Logf("GUI fixture stopped: sent=%d decoded=%d", sent, received)
				return
			}
			if sent >= maxFrames {
				continue
			}
			if _, err := os.Stat(toneFile); err != nil {
				continue
			}
			s := snapshot()
			members, validPeer := 0, false
			for _, user := range s.Users {
				if user.ChannelID == channelID {
					members++
					validPeer = validPeer || user.Nickname == "Resona-GUI-Verify"
				}
			}
			if s.ChannelID != channelID || members != 2 || !validPeer {
				continue
			}
			for i := range pcm {
				pcm[i] = float32(0.01 * math.Sin(2*math.Pi*330*float64(sent*960+i)/48000))
			}
			n, err := encoder.Encode(pcm, encoded)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.SendVoice(encoded[:n], audio.CodecOpusVoice); err != nil {
				t.Fatal(err)
			}
			sent++
			if sent == maxFrames {
				t.Logf("GUI output probe: sent %d low-volume Opus frames", sent)
			}
		}
	}
}
