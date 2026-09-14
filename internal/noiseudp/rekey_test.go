package noiseudp

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func takePacket(t *testing.T, ch <-chan []byte) []byte {
	t.Helper()
	select {
	case p := <-ch:
		return p
	case <-time.After(time.Second):
		t.Fatal("missing packet")
		return nil
	}
}

func forceRekey(c *Conn, byAge bool) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if byAge {
		c.keySince = time.Now().Add(-rekeyAfter)
	} else {
		c.counter = rekeyPackets
	}
}

func TestRekeyLossReorderReplayAndPhaseWrap(t *testing.T) {
	for _, byAge := range []bool{false, true} {
		t.Run(map[bool]string{true: "age", false: "packets"}[byAge], func(t *testing.T) {
			a, b, ab, ba := pair(t)
			if err := a.SendDatagram([]byte("old")); err != nil {
				t.Fatal(err)
			}
			old := takePacket(t, ab)
			if err := a.SendDatagram([]byte("too late")); err != nil {
				t.Fatal(err)
			}
			expired := takePacket(t, ab)
			forceRekey(a, byAge)
			if err := a.SendDatagram([]byte("new")); err != nil {
				t.Fatal(err)
			}
			update := takePacket(t, ab) // Drop the first update announcement.
			if update[0] != keyUpdate|keyPhase {
				t.Fatal("missing key update")
			}
			fresh := takePacket(t, ab)
			b.receive(fresh)
			b.receive(old)
			b.receive(old)
			if len(b.voices) != 2 || string(<-b.voices) != "new" || string(<-b.voices) != "old" {
				t.Fatal("voice continuity/replay failure")
			}
			_ = takePacket(t, ba) // Drop the first confirmation too.
			if err := a.refreshKeys(time.Now().Add(rekeyRetry)); err != nil {
				t.Fatal(err)
			}
			retry := takePacket(t, ab)
			if binary.BigEndian.Uint32(retry[1:5]) <= binary.BigEndian.Uint32(fresh[1:5]) {
				t.Fatal("retry reused nonce")
			}
			b.receive(retry)
			a.receive(takePacket(t, ba))
			a.sendMu.Lock()
			confirmed := a.txConfirmed
			a.sendMu.Unlock()
			if !confirmed || a.Context().Err() != nil || b.Context().Err() != nil {
				t.Fatal("rekey did not confirm")
			}
			// An authenticated but delayed old packet is no longer accepted after grace.
			if _, _, _, ok := b.decryptPacket(expired, time.Now().Add(oldKeyGrace)); ok {
				t.Fatal("expired old key accepted")
			}
			forceRekey(a, false)
			if err := a.SendDatagram([]byte("third")); err != nil {
				t.Fatal(err)
			}
			b.receive(takePacket(t, ab))
			b.receive(takePacket(t, ab))
			confirmedSecond := takePacket(t, ba)
			var stale [8]byte
			binary.BigEndian.PutUint64(stale[:], 1)
			if err := b.send(keyACK, stale[:]); err != nil {
				t.Fatal(err)
			}
			a.receive(takePacket(t, ba))
			a.sendMu.Lock()
			staleConfirmed := a.txConfirmed
			a.sendMu.Unlock()
			if staleConfirmed {
				t.Fatal("old generation confirmation accepted")
			}
			a.receive(confirmedSecond)
			b.receive(old)   // Phase wrapped, but the epoch-0 key must never work again.
			b.receive(fresh) // Duplicate from previous generation must also fail.
			if b.rxEpoch != 2 || len(b.voices) != 1 || string(<-b.voices) != "third" {
				t.Fatal("phase wrap reset session or admitted old replay")
			}
		})
	}
}

func TestRekeyForgeryDoesNotAdvanceKeys(t *testing.T) {
	a, b, ab, ba := pair(t)
	forceRekey(a, false)
	_ = a.SendDatagram([]byte("new"))
	_ = takePacket(t, ab)
	p := takePacket(t, ab)
	bad := append([]byte(nil), p...)
	bad[len(bad)-1] ^= 1
	b.receive(bad)
	if b.rxEpoch != 0 || b.replay.highest != 0 || len(ba) != 0 {
		t.Fatal("forgery changed receive state")
	}
	b.receive(p)
	if b.rxEpoch != 1 || len(b.voices) != 1 {
		t.Fatal("valid update lost after forgery")
	}
}

func TestSimultaneousRekeyReliableControlAndVoice(t *testing.T) {
	a, b, ab, ba := pair(t)
	forceRekey(a, true)
	forceRekey(b, false)
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	var updateDrops, confirmationDrops atomic.Int32
	pump := func(src <-chan []byte, dst *Conn) {
		defer workers.Done()
		dropped := map[byte]bool{}
		for {
			select {
			case <-ctx.Done():
				return
			case p := <-src:
				kind := p[0] &^ keyPhase
				if (kind == keyUpdate || kind == keyACK || kind == control || kind == ack) && !dropped[kind] {
					dropped[kind] = true
					if kind == keyUpdate {
						updateDrops.Add(1)
					}
					if kind == keyACK {
						confirmationDrops.Add(1)
					}
					continue
				}
				dst.receive(p)
			}
		}
	}
	workers.Add(2)
	go pump(ab, b)
	go pump(ba, a)
	t.Cleanup(func() { cancel(); workers.Wait() })
	data := bytes.Repeat([]byte("same stream across keys"), 150)
	_ = a.SetWriteDeadline(time.Now().Add(4 * time.Second))
	_ = b.SetReadDeadline(time.Now().Add(4 * time.Second))
	written := make(chan error, 1)
	go func() { _, err := a.Write(data); written <- err }()
	if err := a.SendDatagram([]byte("audio")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(data))
	if _, err := io.ReadFull(b, got); err != nil {
		t.Fatal(err)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("control corrupted across rekey")
	}
	voiceCtx, voiceCancel := context.WithTimeout(ctx, time.Second)
	defer voiceCancel()
	v, err := b.ReceiveDatagram(voiceCtx)
	if err != nil || string(v) != "audio" {
		t.Fatalf("voice interrupted: %q %v", v, err)
	}
	for _, c := range []*Conn{a, b} {
		if err := c.refreshKeys(time.Now().Add(rekeyRetry)); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(time.Second)
	for {
		a.sendMu.Lock()
		ac := a.txConfirmed
		a.sendMu.Unlock()
		b.sendMu.Lock()
		bc := b.txConfirmed
		b.sendMu.Unlock()
		if ac && bc {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("simultaneous updates did not confirm")
		}
		time.Sleep(time.Millisecond)
	}
	if updateDrops.Load() != 2 || confirmationDrops.Load() != 2 {
		t.Fatal("missing loss injection")
	}
}

func TestUnconfirmedRekeyExpiresAndUnblocksReader(t *testing.T) {
	a, _, ab, _ := pair(t)
	forceRekey(a, true)
	if err := a.refreshKeys(time.Now()); err != nil {
		t.Fatal(err)
	}
	_ = takePacket(t, ab)
	done := make(chan error, 1)
	go func() { _, err := a.Read(make([]byte, 1)); done <- err }()
	if err := a.refreshKeys(time.Now().Add(rekeyTimeout)); err == nil {
		t.Fatal("unconfirmed keys survived timeout")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("reader succeeded after close")
		}
	case <-time.After(time.Second):
		t.Fatal("reader stuck")
	}
}

func TestManyRekeysOnRealUDPKeepSameSession(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := PublicKey(key)
	l, err := Listen("127.0.0.1:0", key, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, err := Dial(ctx, l.Addr().String(), pub)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	b, err := l.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := a.LocalAddr().String()
	for generation := uint64(1); generation <= 32; generation++ {
		// Logical aging exercises over 24 hours without a day-long wall-clock test.
		forceRekey(a, true)
		forceRekey(b, true)
		data := []byte{byte(generation)}
		if err := a.SendDatagram(data); err != nil {
			t.Fatal(err)
		}
		if err := b.SendDatagram(data); err != nil {
			t.Fatal(err)
		}
		for _, c := range []*Conn{a, b} {
			got, err := c.ReceiveDatagram(ctx)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("generation %d: voice %q %v", generation, got, err)
			}
		}
		_ = a.SetWriteDeadline(time.Now().Add(time.Second))
		_ = b.SetReadDeadline(time.Now().Add(time.Second))
		written := make(chan error, 1)
		go func() { _, err := a.Write(data); written <- err }()
		got := make([]byte, 1)
		if _, err := io.ReadFull(b, got); err != nil {
			t.Fatal(err)
		}
		if err := <-written; err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, data) {
			t.Fatal("stream reset or corrupted")
		}
		deadline := time.Now().Add(time.Second)
		for {
			a.sendMu.Lock()
			ac, ae := a.txConfirmed, a.txEpoch
			a.sendMu.Unlock()
			b.sendMu.Lock()
			bc, be := b.txConfirmed, b.txEpoch
			b.sendMu.Unlock()
			if ac && bc && ae == generation && be == generation {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("generation %d not confirmed", generation)
			}
			time.Sleep(time.Millisecond)
		}
	}
	l.mu.Lock()
	same := len(l.peers) == 1 && l.peers[endpoint] != nil && l.peers[endpoint].conn == b
	l.mu.Unlock()
	if !same || a.LocalAddr().String() != endpoint || a.Context().Err() != nil {
		t.Fatal("rekey replaced or closed session")
	}
}

func TestRekeyNonceSpaceAndGenerationLimit(t *testing.T) {
	if packetNonce(1, 1) <= packetNonce(0, maxCounter-1) || packetNonce(maxKeyEpoch, maxCounter-1) >= ^uint64(0)-1 {
		t.Fatal("nonce wrapped or used reserved value")
	}
	a, _, ab, _ := pair(t)
	a.sendMu.Lock()
	a.txEpoch = maxKeyEpoch
	a.counter = rekeyPackets
	a.sendMu.Unlock()
	if err := a.SendDatagram(nil); err == nil || len(ab) != 0 {
		t.Fatal("generation limit did not fail closed")
	}
}

func TestCloseDuringRekeyCannotBeRevived(t *testing.T) {
	a, b, ab, ba := pair(t)
	forceRekey(a, false)
	if err := a.refreshKeys(time.Now()); err != nil {
		t.Fatal(err)
	}
	b.receive(takePacket(t, ab))
	confirmation := takePacket(t, ba)
	if err := a.CloseWithError(0, ""); err != nil {
		t.Fatal(err)
	}
	b.receive(takePacket(t, ab))
	a.receive(confirmation)
	if a.Context().Err() == nil || b.Context().Err() == nil {
		t.Fatal("close lost during update")
	}
	if err := a.refreshKeys(time.Now().Add(rekeyRetry)); err == nil || len(ab) != 0 {
		t.Fatal("late update revived closed session")
	}
}
