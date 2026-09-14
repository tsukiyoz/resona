package nativewire

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

type QueuedVoice struct {
	Data []byte
	At   time.Time
}

// quic-go SendDatagram can block on its bounded internal queue. Keep it off
// capture/state loops, discard stale local work and terminate stalled sessions.
func SendVoiceQueue(c Connection, queue <-chan QueuedVoice) {
	sendVoiceSource(c, func(ctx context.Context) (QueuedVoice, bool) {
		select {
		case <-ctx.Done():
			return QueuedVoice{}, false
		case packet, ok := <-queue:
			return packet, ok
		}
	})
}

func sendVoiceSource(c Connection, next func(context.Context) (QueuedVoice, bool)) {
	ctx := c.Context()
	guard := newVoiceSendGuard(c, 250*time.Millisecond)
	defer guard.stop()
	for {
		packet, ok := next(ctx)
		if !ok {
			return
		}
		if time.Since(packet.At) > 100*time.Millisecond {
			continue
		}
		if !guard.begin() {
			return
		}
		err := c.SendDatagram(packet.Data)
		if !guard.end() {
			return
		}
		if err != nil {
			_ = c.CloseWithError(1, "voice transport failed")
			return
		}
	}
}

// A timer callback may already be running when Stop/Reset is called. Serialize
// its decision with begin/end and check the current deadline, so an old callback
// cannot close a healthy later send. Once tripped, no new send can begin.
type voiceSendGuard struct {
	mu       sync.Mutex
	c        Connection
	timeout  time.Duration
	deadline time.Time
	tripped  bool
	stopped  bool
	timer    *time.Timer
	unwatch  func() bool
}

func newVoiceSendGuard(c Connection, timeout time.Duration) *voiceSendGuard {
	g := &voiceSendGuard{c: c, timeout: timeout}
	g.timer = time.AfterFunc(time.Hour, func() { g.expire(false) })
	g.timer.Stop()
	g.unwatch = context.AfterFunc(c.Context(), func() { g.expire(true) })
	return g
}

func (g *voiceSendGuard) expire(cancelled bool) {
	g.mu.Lock()
	closeConn := !g.stopped && !g.tripped && !g.deadline.IsZero() &&
		(cancelled || !time.Now().Before(g.deadline))
	if closeConn {
		g.tripped = true
	}
	g.mu.Unlock()
	if closeConn {
		_ = g.c.CloseWithError(1, "voice transport stalled")
	}
}

func (g *voiceSendGuard) begin() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped || g.tripped || g.c.Context().Err() != nil {
		return false
	}
	g.deadline = time.Now().Add(g.timeout)
	g.timer.Reset(g.timeout)
	return true
}

func (g *voiceSendGuard) end() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.timer.Stop()
	g.deadline = time.Time{}
	return !g.tripped && g.c.Context().Err() == nil
}

func (g *voiceSendGuard) stop() {
	g.mu.Lock()
	g.stopped = true
	g.deadline = time.Time{}
	g.timer.Stop()
	g.mu.Unlock()
	g.unwatch()
}

func QUICConfig(server bool) *quic.Config {
	streams := int64(-1)
	if server {
		streams = 1
	}
	return &quic.Config{EnableDatagrams: true, MaxIncomingStreams: streams, MaxIncomingUniStreams: -1,
		HandshakeIdleTimeout: 5 * time.Second, MaxIdleTimeout: 30 * time.Second, KeepAlivePeriod: 10 * time.Second,
		InitialStreamReceiveWindow: MaxFrame, MaxStreamReceiveWindow: 2 * MaxFrame,
		InitialConnectionReceiveWindow: 2 * MaxFrame, MaxConnectionReceiveWindow: 4 * MaxFrame}
}

func Fingerprint(der []byte) string { sum := sha256.Sum256(der); return hex.EncodeToString(sum[:]) }

// An explicit pin replaces PKI hostname/issuer trust, never proof of possession
// or validity dates. Empty pins retain standard TLS verification.
func ClientTLS(pin string) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, NextProtos: []string{ALPN}}
	if pin == "" {
		return cfg, nil
	}
	want, err := hex.DecodeString(pin)
	if err != nil || len(want) != 32 {
		return nil, errors.New("invalid certificate fingerprint")
	}
	cfg.InsecureSkipVerify = true
	cfg.VerifyConnection = func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			return errors.New("missing server certificate")
		}
		cert := state.PeerCertificates[0]
		got := sha256.Sum256(cert.Raw)
		if subtle.ConstantTimeCompare(got[:], want) != 1 {
			return errors.New("certificate fingerprint mismatch")
		}
		roots := x509.NewCertPool()
		roots.AddCert(cert)
		_, err := cert.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
		return err
	}
	return cfg, nil
}
