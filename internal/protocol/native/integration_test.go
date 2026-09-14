package native

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"math/big"
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

func startServer(t *testing.T, useNoise ...bool) (client.ServerProfile, context.CancelFunc, <-chan error) {
	return startServerOwned(t, nil, useNoise...)
}

func startServerOwned(t *testing.T, ownership *server.Ownership, useNoise ...bool) (client.ServerProfile, context.CancelFunc, <-chan error) {
	return startServerConfigured(t, server.Config{Name: "test", Password: "test-password", Channels: []w.Channel{{ID: 1, Name: "one"}, {ID: 2, Name: "two"}}, Ownership: ownership}, useNoise...)
}

func startServerConfigured(t *testing.T, cfg server.Config, useNoise ...bool) (client.ServerProfile, context.CancelFunc, <-chan error) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if len(useNoise) > 0 && useNoise[0] {
		cfg.NoiseKey, err = noiseudp.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
	}
	s, err := server.Listen("127.0.0.1:0", cfg, &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	t.Cleanup(cancel)
	p := client.ServerProfile{Protocol: "resona", Address: s.Addr().String(), Nickname: "test", CertificateFingerprint: w.Fingerprint(der)}
	if len(cfg.NoiseKey) > 0 {
		pub, _ := noiseudp.PublicKey(cfg.NoiseKey)
		p.Protocol = "resona-noise"
		p.CertificateFingerprint = ""
		p.ServerPublicKey = hex.EncodeToString(pub)
	}
	return p, cancel, done
}

type observer struct {
	mu       sync.Mutex
	state    client.RemoteState
	messages []client.RemoteMessage
}

func (o *observer) update(s client.RemoteState) {
	o.mu.Lock()
	o.state = s
	o.messages = append(o.messages, s.Messages...)
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
func TestNativeQUICChatVoiceIsolationAndShutdown(t *testing.T) {
	for _, mode := range []bool{false, true} {
		name := "quic"
		if mode {
			name = "noise"
		}
		t.Run(name, func(t *testing.T) { testChatVoiceIsolationAndShutdown(t, mode) })
	}
}
func testChatVoiceIsolationAndShutdown(t *testing.T, useNoise bool) {
	p, stop, done := startServer(t, useNoise)
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
	for _, mode := range []string{"pin", "system", "password", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			q := p
			password := "test-password"
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			switch mode {
			case "pin":
				q.CertificateFingerprint = strings.Repeat("0", 64)
			case "system":
				q.CertificateFingerprint = ""
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
	p, stop, done := startServer(t, true)
	defer func() { stop(); <-done }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if c, e := (Connector{}).Connect(ctx, p, "wrong-password", func(client.RemoteState) {}); e == nil {
		c.Close()
		t.Fatal("wrong password accepted over Noise")
	}
}
