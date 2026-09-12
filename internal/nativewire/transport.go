package nativewire

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
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
	for {
		select {
		case <-c.Context().Done():
			return
		case packet := <-queue:
			if time.Since(packet.At) > 100*time.Millisecond {
				continue
			}
			ctx, cancel := context.WithTimeout(c.Context(), 250*time.Millisecond)
			stop := context.AfterFunc(ctx, func() { _ = c.CloseWithError(1, "voice transport stalled") })
			err := c.SendDatagram(packet.Data)
			stop()
			cancel()
			if err != nil {
				_ = c.CloseWithError(1, "voice transport failed")
				return
			}
		}
	}
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
