package native

import (
	"context"
	"encoding/hex"
	"strings"
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

func startServer(t *testing.T) (client.ServerProfile, context.CancelFunc, <-chan error) {
	return startServerOwned(t, nil)
}

func startServerOwned(t *testing.T, ownership *server.Ownership) (client.ServerProfile, context.CancelFunc, <-chan error) {
	return startServerConfigured(t, server.Config{Name: "test", Password: "test-password", Channels: []w.Channel{{ID: 1, Name: "one"}, {ID: 2, Name: "two"}}, Ownership: ownership})
}

func startServerConfigured(t *testing.T, cfg server.Config) (client.ServerProfile, context.CancelFunc, <-chan error) {
	t.Helper()
	var err error
	cfg.NoiseKey, err = noiseudp.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	s, err := server.Listen("127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	t.Cleanup(cancel)
	pub, _ := noiseudp.PublicKey(cfg.NoiseKey)
	p := client.ServerProfile{Protocol: "resona-noise", Address: s.Addr().String(), Nickname: "test", ServerPublicKey: hex.EncodeToString(pub)}
	return p, cancel, done
}

type observer struct {
	mu       sync.Mutex
	state    client.RemoteState
	messages []client.RemoteMessage
	events   []client.RemoteEvent
}

func (o *observer) update(s client.RemoteState) {
	o.mu.Lock()
	o.state = s
	o.messages = append(o.messages, s.Messages...)
	o.events = append(o.events, s.Events...)
	o.mu.Unlock()
}
func (o *observer) snapshot() client.RemoteState { o.mu.Lock(); defer o.mu.Unlock(); return o.state }
func connectTest(t *testing.T, p client.ServerProfile) (*connection, *observer) {
	t.Helper()
	o := &observer{}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	remote, err := (Connector{}).Connect(ctx, p, "test-password", o.update)
	if err != nil {
		t.Fatal(err)
	}
	c := remote.(*connection)
	t.Cleanup(func() { _ = c.Close() })
	if err := c.SetResourceInterest(ctx, true, true); err != nil {
		t.Fatal(err)
	}
	return c, o
}
func eventually(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition did not become true")
}
func TestNativeChatVoiceIsolationAndShutdown(t *testing.T) {
	p, stop, done := startServer(t)
	a, ao := connectTest(t, p)
	p.Nickname = "B"
	b, bo := connectTest(t, p)
	p.Nickname = "C"
	c, co := connectTest(t, p)
	eventually(t, func() bool { return len(ao.snapshot().Users) == 3 && len(bo.snapshot().Users) == 3 })
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := c.MoveChannel(ctx, "2"); err != nil {
		t.Fatal(err)
	}
	if co.snapshot().ChannelID != "2" {
		t.Fatal("move returned before state")
	}
	if err := a.SendChannelMessage(ctx, "1", "hello"); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { bo.mu.Lock(); defer bo.mu.Unlock(); return len(bo.messages) == 1 })
	co.mu.Lock()
	n := len(co.messages)
	co.mu.Unlock()
	if n != 0 {
		t.Fatal("cross-channel text")
	}
	if err := a.SendChannelMessage(ctx, "2", "wrong"); err == nil {
		t.Fatal("wrong channel message accepted")
	}
	if err := a.SetVoiceMuted(ctx, false, false); err != nil {
		t.Fatal(err)
	}
	received := make(chan audio.Packet, 16)
	b.SetVoiceHandler(func(p audio.Packet) {
		select {
		case received <- p:
		default:
		}
	})
	selfVoice := make(chan audio.Packet, 1)
	a.SetVoiceHandler(func(p audio.Packet) {
		select {
		case selfVoice <- p:
		default:
		}
	})
	otherVoice := make(chan audio.Packet, 1)
	c.SetVoiceHandler(func(p audio.Packet) {
		select {
		case otherVoice <- p:
		default:
		}
	})
	encoder, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 1, Application: gopus.ApplicationVoIP})
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 1024)
	pcm := make([]float32, 960)
	for i := range pcm {
		pcm[i] = float32(i%48) / 240
	}
	size, err := encoder.Encode(pcm, data)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.SendVoice(data[:size], audio.CodecOpusVoice); err != nil {
		t.Fatal(err)
	}
	var packet audio.Packet
	select {
	case packet = <-received:
	case <-ctx.Done():
		t.Fatal("voice not received")
	}
	decoder, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatal(err)
	}
	if n, err := decoder.Decode(packet.Data, make([]float32, 960)); err != nil || n != 960 {
		t.Fatalf("decode %d %v", n, err)
	}
	if packet.SenderID == 0 || packet.Instance == "" {
		t.Fatal("missing sender identity")
	}
	if err = a.SendVoice(nil, audio.CodecOpusVoice); err != nil {
		t.Fatal(err)
	}
	select {
	case packet = <-received:
		if !packet.End {
			t.Fatal("missing end")
		}
	case <-ctx.Done():
		t.Fatal("end not received")
	}
	select {
	case <-selfVoice:
		t.Fatal("self echo")
	case <-otherVoice:
		t.Fatal("cross-channel voice")
	case <-time.After(80 * time.Millisecond):
	}
	if err := b.SetVoiceMuted(ctx, true, true); err != nil {
		t.Fatal(err)
	}
	if err := a.SendVoice(data[:size], audio.CodecOpusVoice); err != nil {
		t.Fatal(err)
	}
	select {
	case <-received:
		t.Fatal("deafened listener received voice")
	case <-time.After(80 * time.Millisecond):
	}
	if err := b.SetVoiceMuted(ctx, true, false); err != nil {
		t.Fatal(err)
	}
	if err := a.SetVoiceMuted(ctx, true, false); err != nil {
		t.Fatal(err)
	}
	if err := a.SendVoice(data[:size], audio.CodecOpusVoice); err != nil {
		t.Fatal(err)
	}
	select {
	case <-received:
		t.Fatal("server relayed muted sender")
	case <-time.After(80 * time.Millisecond):
	}
	if err := a.SetVoiceMuted(ctx, false, false); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	oldEpoch := a.state.Epoch
	a.mu.Unlock()
	if err = a.MoveChannel(ctx, "2"); err != nil {
		t.Fatal(err)
	}
	stale, _ := w.EncodeVoice(w.Voice{Epoch: oldEpoch, Sequence: 12, Data: data[:size]}, false)
	if err = a.conn.SendDatagram(stale); err != nil {
		t.Fatal(err)
	}
	select {
	case <-otherVoice:
		t.Fatal("stale upload relayed after move")
	case <-time.After(80 * time.Millisecond):
	}
	oldID := bo.snapshot().SelfID
	_ = b.Close()
	eventually(t, func() bool { return len(ao.snapshot().Users) == 2 })
	d, do := connectTest(t, p)
	_ = d
	if do.snapshot().SelfID == oldID {
		t.Fatal("reused member ID")
	}
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server shutdown blocked")
	}
	eventually(t, func() bool { return ao.snapshot().Closed })
}
func TestNativeTrustPasswordAndCancelledSetup(t *testing.T) {
	p, stop, done := startServer(t)
	defer func() { stop(); <-done }()
	for _, mode := range []string{"pin", "missing", "password", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			q := p
			password := "test-password"
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			switch mode {
			case "pin":
				q.ServerPublicKey = strings.Repeat("0", 64)
			case "missing":
				q.ServerPublicKey = ""
			case "password":
				password = "bad"
			case "cancel":
				cancel()
			}
			c, err := (Connector{}).Connect(ctx, q, password, func(client.RemoteState) {})
			if c != nil {
				_ = c.Close()
			}
			if err == nil {
				t.Fatal("unauthenticated setup accepted")
			}
		})
	}
}

func TestNoisePasswordRejection(t *testing.T) {
	p, stop, done := startServer(t)
	defer func() { stop(); <-done }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if c, e := (Connector{}).Connect(ctx, p, "wrong-password", func(client.RemoteState) {}); e == nil {
		c.Close()
		t.Fatal("wrong password accepted over Noise")
	}
}
