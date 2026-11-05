package serverauth

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

type recordedClient struct {
	io.ReadWriter
	sent bytes.Buffer
}

func (r *recordedClient) Write(p []byte) (int, error) {
	n, err := r.ReadWriter.Write(p)
	_, _ = r.sent.Write(p[:n])
	return n, err
}

func TestRecordedProofCannotAuthenticateAgain(t *testing.T) {
	s, err := New("secret")
	if err != nil {
		t.Fatal(err)
	}
	binding := make([]byte, 32)
	a, b := net.Pipe()
	_ = a.SetDeadline(time.Now().Add(time.Second))
	_ = b.SetDeadline(time.Now().Add(time.Second))
	done := make(chan error, 1)
	go func() { defer a.Close(); done <- s.Accept(a, binding) }()
	client := &recordedClient{ReadWriter: b}
	if err := Authenticate(client, binding, "secret", false); err != nil {
		t.Fatal(err)
	}
	_ = b.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if client.sent.Len() != ke1Size+ke3Size {
		t.Fatal("unexpected PAKE transcript size")
	}
	a, b = net.Pipe()
	defer b.Close()
	_ = a.SetDeadline(time.Now().Add(time.Second))
	_ = b.SetDeadline(time.Now().Add(time.Second))
	go func() { defer a.Close(); done <- s.Accept(a, binding) }()
	mode := make([]byte, 1)
	if _, err = io.ReadFull(b, mode); err != nil {
		t.Fatal(err)
	}
	if err = write(b, client.sent.Bytes()[:ke1Size]); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, ke2Size)
	if _, err = io.ReadFull(b, response); err != nil {
		t.Fatal(err)
	}
	if err = write(b, client.sent.Bytes()[ke1Size:]); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, ErrAuthentication) {
		t.Fatalf("replay accepted: %v", err)
	}
}

func TestConcurrentAuthentication(t *testing.T) {
	s, err := New("secret")
	if err != nil {
		t.Fatal(err)
	}
	for range 8 {
		t.Run("connection", func(t *testing.T) {
			t.Parallel()
			a, b := net.Pipe()
			defer b.Close()
			_ = a.SetDeadline(time.Now().Add(5 * time.Second))
			_ = b.SetDeadline(time.Now().Add(5 * time.Second))
			binding := make([]byte, 32)
			done := make(chan error, 1)
			go func() { defer a.Close(); done <- s.Accept(a, binding) }()
			if err := Authenticate(b, binding, "secret", false); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func BenchmarkPasswordHandshake(b *testing.B) {
	s, err := New("benchmark-only")
	if err != nil {
		b.Fatal(err)
	}
	binding := make([]byte, 32)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		server, client := net.Pipe()
		done := make(chan error, 1)
		go func() { defer server.Close(); done <- s.Accept(server, binding) }()
		err := Authenticate(client, binding, "benchmark-only", false)
		_ = client.Close()
		if err != nil {
			b.Fatal(err)
		}
		if err := <-done; err != nil {
			b.Fatal(err)
		}
	}
}

func TestAuthentication(t *testing.T) {
	for _, tc := range []struct {
		name, server, client string
		pinned, changed, ok  bool
	}{
		{"password", "secret", "secret", false, false, true},
		{"pinned-password", "secret", "secret", true, false, true},
		{"wrong-password", "secret", "wrong", false, false, false},
		{"other-channel", "secret", "secret", false, true, false},
		{"public-pinned", "", "", true, false, true},
		{"public-untrusted", "", "", false, false, false},
		{"no-downgrade", "", "secret", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New(tc.server)
			if err != nil {
				t.Fatal(err)
			}
			data, err := s.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			s, err = Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			a, b := net.Pipe()
			defer a.Close()
			defer b.Close()
			_ = a.SetDeadline(time.Now().Add(time.Second))
			_ = b.SetDeadline(time.Now().Add(time.Second))
			binding := make([]byte, 32)
			done := make(chan error, 1)
			go func() { defer a.Close(); done <- s.Accept(a, binding) }()
			other := append([]byte(nil), binding...)
			if tc.changed {
				other[0] = 1
			}
			err = Authenticate(b, other, tc.client, tc.pinned)
			_ = b.Close()
			serverErr := <-done
			if (err == nil) != tc.ok || (tc.ok && serverErr != nil) {
				t.Fatalf("client=%v server=%v want success=%v", err, serverErr, tc.ok)
			}
		})
	}
}
