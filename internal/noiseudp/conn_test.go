package noiseudp

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func pair(t *testing.T) (*Conn, *Conn, chan []byte, chan []byte) {
	t.Helper()
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := PublicKey(key)
	a, _ := handshake(true, pub)
	b, _ := handshake(false, key)
	m, _, _, err := a.WriteMessage(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = b.ReadMessage(nil, m); err != nil {
		t.Fatal(err)
	}
	m, ab, ba, err := b.WriteMessage(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, at, ar, err := a.ReadMessage(nil, m)
	if err != nil {
		t.Fatal(err)
	}
	q1, q2 := make(chan []byte, 256), make(chan []byte, 256)
	addr := &net.UDPAddr{}
	c1 := newConn(at, ar, addr, addr, func(p []byte) error { q1 <- append([]byte(nil), p...); return nil }, nil)
	c2 := newConn(ba, ab, addr, addr, func(p []byte) error { q2 <- append([]byte(nil), p...); return nil }, nil)
	t.Cleanup(func() { c1.fail(net.ErrClosed); c2.fail(net.ErrClosed); <-c1.done; <-c2.done })
	return c1, c2, q1, q2
}
func TestDatagramsAuthenticateReorderAndReplay(t *testing.T) {
	a, b, out, _ := pair(t)
	_ = a.SendDatagram([]byte("first"))
	p1 := <-out
	_ = a.SendDatagram([]byte("second"))
	p2 := <-out
	bad := append([]byte(nil), p2...)
	binary.BigEndian.PutUint32(bad[1:5], 900)
	b.receive(bad)
	if b.replay.highest != 0 {
		t.Fatal("unauthenticated counter advanced replay window")
	}
	b.receive(p2)
	b.receive(p1)
	b.receive(p1)
	if len(b.voices) != 2 {
		t.Fatal("lost reordered packet or accepted replay")
	}
	if string(<-b.voices) != "second" || string(<-b.voices) != "first" {
		t.Fatal("bad plaintext")
	}
	bad = append([]byte(nil), p2...)
	bad[0] = closing
	b.receive(bad)
	if b.ctx.Err() != nil {
		t.Fatal("unauthenticated close accepted")
	}
	_, other, _, _ := pair(t)
	other.receive(p1)
	if len(other.voices) != 0 {
		t.Fatal("old session packet accepted")
	}
}
func TestReliableControlRetriesWithoutDuplicateDelivery(t *testing.T) {
	a, b, ab, ba := pair(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var droppedData, droppedACK atomic.Bool
	finished := make(chan struct{}, 2)
	go func() {
		defer func() { finished <- struct{}{} }()
		for {
			select {
			case <-ctx.Done():
				return
			case p := <-ab:
				if p[0] == control && !droppedData.Swap(true) {
					continue
				}
				b.receive(p)
			}
		}
	}()
	go func() {
		defer func() { finished <- struct{}{} }()
		for {
			select {
			case <-ctx.Done():
				return
			case p := <-ba:
				if p[0] == ack && !droppedACK.Swap(true) {
					continue
				}
				a.receive(p)
			}
		}
	}()
	t.Cleanup(func() { cancel(); <-finished; <-finished })
	data := bytes.Repeat([]byte("control-message"), 300)
	_ = a.SetWriteDeadline(time.Now().Add(4 * time.Second))
	_ = b.SetReadDeadline(time.Now().Add(4 * time.Second))
	done := make(chan error, 1)
	go func() { _, e := a.Write(data); done <- e }()
	got := make([]byte, len(data))
	if _, e := io.ReadFull(b, got); e != nil {
		t.Fatal(e)
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(data, got) || !droppedData.Load() || !droppedACK.Load() {
		t.Fatal("control corruption or missing fault injection")
	}
	_ = b.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
	if _, e := b.Read(make([]byte, 1)); e == nil {
		t.Fatal("duplicate control delivered")
	}
}
func TestNonceBudgetAndReadCancellation(t *testing.T) {
	a, _, out, _ := pair(t)
	a.sendMu.Lock()
	a.counter = maxCounter - 2
	a.txConfirmed = false
	a.updateSent = time.Now()
	a.sendMu.Unlock()
	if e := a.SendDatagram([]byte{1}); e != nil {
		t.Fatal(e)
	}
	last := <-out
	if binary.BigEndian.Uint32(last[1:5]) != maxCounter-1 {
		t.Fatal("bad final counter")
	}
	if e := a.SendDatagram([]byte{2}); e == nil {
		t.Fatal("counter wrapped")
	}
	if len(out) != 0 {
		t.Fatal("packet sent after key limit")
	}
	_, b, _, _ := pair(t)
	done := make(chan error, 1)
	go func() { _, e := b.Read(make([]byte, 1)); done <- e }()
	_ = b.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("read deadline ignored")
		}
	case <-time.After(time.Second):
		t.Fatal("deadline did not wake read")
	}
}

func TestCookieAndCancelledHandshake(t *testing.T) {
	key, _ := GenerateKey()
	pub, _ := PublicKey(key)
	l, e := Listen("127.0.0.1:0", key, 2)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	msg := bytes.Repeat([]byte{1}, 48)
	token := l.cookie("127.0.0.1:1", msg, 1)
	if bytes.Equal(token, l.cookie("127.0.0.1:2", msg, 1)) {
		t.Fatal("cookie not address bound")
	}
	msg[0]++
	if bytes.Equal(token, l.cookie("127.0.0.1:1", msg, 1)) {
		t.Fatal("cookie not handshake bound")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c, e := Dial(ctx, l.Addr().String(), pub); e == nil {
		c.Close()
		t.Fatal("cancelled dial succeeded")
	}
	wrong, _ := GenerateKey()
	wrongPub, _ := PublicKey(wrong)
	ctx, cancel = context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if c, e := Dial(ctx, l.Addr().String(), wrongPub); e == nil {
		c.Close()
		t.Fatal("wrong server key accepted")
	}
}
