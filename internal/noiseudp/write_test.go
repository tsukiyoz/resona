package noiseudp

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestListenerWriteGateTimeoutAndCancellation(t *testing.T) {
	for _, mode := range []string{"timeout", "cancel-wait", "already-cancelled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			l := &Listener{ctx: ctx, writeGate: make(chan struct{}, 1)}
			if mode != "already-cancelled" {
				l.writeGate <- struct{}{}
			} else {
				cancel()
			}
			done := make(chan error, 1)
			go func() { done <- l.write(nil, nil) }()
			if mode == "cancel-wait" {
				cancel()
			}
			select {
			case err := <-done:
				if mode == "timeout" {
					if err == nil || err.Error() != "UDP writer busy" {
						t.Fatalf("error=%v", err)
					}
				} else if !errors.Is(err, net.ErrClosed) {
					t.Fatalf("error=%v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("write did not terminate")
			}
		})
	}
}

func TestListenerWriteReleasesGate(t *testing.T) {
	u, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	r, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	l := &Listener{ctx: context.Background(), u: u, writeGate: make(chan struct{}, 1)}
	for i := 0; i < 2; i++ {
		if err := l.write([]byte{byte(i)}, r.LocalAddr().(*net.UDPAddr)); err != nil {
			t.Fatal(err)
		}
		_ = r.SetReadDeadline(time.Now().Add(time.Second))
		var b [8]byte
		n, _, err := r.ReadFromUDP(b[:])
		if err != nil || n != 1 || b[0] != byte(i) {
			t.Fatalf("receive n=%d err=%v", n, err)
		}
	}
	_ = u.Close()
	if err := l.write(nil, r.LocalAddr().(*net.UDPAddr)); err == nil {
		t.Fatal("closed socket write succeeded")
	}
	if len(l.writeGate) != 0 {
		t.Fatal("failed write retained gate")
	}
}

func TestListenerWriteResetsExpiredPooledTimer(t *testing.T) {
	u, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	r, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	expired := time.NewTimer(time.Nanosecond)
	<-expired.C
	entered := make(chan struct{})
	l := &Listener{ctx: context.Background(), u: u, writeGate: make(chan struct{}, 1)}
	l.writeTimers.New = func() any {
		close(entered)
		return expired
	}
	l.writeGate <- struct{}{}
	done := make(chan error, 1)
	go func() { done <- l.write([]byte("after timeout"), r.LocalAddr().(*net.UDPAddr)) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not wait for gate")
	}
	<-l.writeGate
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("writer did not resume")
	}
	_ = r.SetReadDeadline(time.Now().Add(time.Second))
	var data [32]byte
	n, _, err := r.ReadFromUDP(data[:])
	if err != nil || string(data[:n]) != "after timeout" || len(l.writeGate) != 0 {
		t.Fatalf("write after timer reset: n=%d err=%v gate=%d", n, err, len(l.writeGate))
	}
}
