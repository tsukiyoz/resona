package server

import (
	"bytes"
	"testing"
	"time"

	w "github.com/tsukiyoz/resona/internal/nativewire"
)

func TestPokeIsTransientScopedAndRateLimited(t *testing.T) {
	newPeer := func(id uint16, channel uint16) *peer {
		return &peer{member: w.Member{ID: id, Channel: channel, Instance: "instance", Nickname: "nickname"}, receivePokes: true, out: make(chan []byte, 16)}
	}
	a, b, c := newPeer(1, 1), newPeer(2, 2), newPeer(3, 1)
	s := &Server{peers: map[uint16]*peer{1: a, 2: b, 3: c}}
	poke := func(sender *peer, target uint16, instance string) uint8 {
		t.Helper()
		packet, err := w.Pack(w.PokeKind, 1, w.Poke{Target: target, Instance: instance})
		if err != nil {
			t.Fatal(err)
		}
		frame, err := w.Read(bytes.NewReader(packet))
		if err != nil || !s.pokeCommand(sender, frame) {
			t.Fatal("poke rejected as malformed", err)
		}
		reply, err := w.Read(bytes.NewReader(<-sender.out))
		var result w.Reply
		if err != nil || w.Decode(reply, &result) != nil {
			t.Fatal("invalid reply", err)
		}
		return result.Code
	}
	if code := poke(a, 2, "instance"); code != w.OK {
		t.Fatal(code)
	}
	frame, err := w.Read(bytes.NewReader(<-b.out))
	var event w.Poked
	if err != nil || w.Decode(frame, &event) != nil || event.Sender != 1 || event.Nickname != a.member.Nickname || frame.Request != 0 {
		t.Fatal("cross-channel attention lost sender authority", err, event)
	}
	if code := poke(a, 2, "instance"); code != w.RateLimited {
		t.Fatal("sender flood", code)
	}
	if code := poke(c, 2, "instance"); code != w.RateLimited {
		t.Fatal("recipient flood", code)
	}
	for _, target := range []uint16{1, 9} {
		if code := poke(a, target, "instance"); code != w.MemberUnavailable {
			t.Fatal("self/offline target", code)
		}
	}
	if code := poke(a, 2, "stale"); code != w.MemberUnavailable {
		t.Fatal("stale instance", code)
	}
	b.receivePokes = false
	if code := poke(a, 2, "instance"); code != w.MemberUnavailable {
		t.Fatal("unsupported receiver", code)
	}
	b.receivePokes = true
	b.removed = true
	if code := poke(a, 2, "instance"); code != w.MemberUnavailable {
		t.Fatal("removed receiver", code)
	}
	b.removed = false
	a.pokeSend.at = time.Now().Add(-6 * time.Second)
	b.pokeReceive.at = time.Now().Add(-3 * time.Second)
	if code := poke(a, 2, "instance"); code != w.OK {
		t.Fatal("cooldown did not recover", code)
	}
	<-b.out
	b.out = make(chan []byte)
	a.pokeSend = bucket{}
	b.pokeReceive = bucket{}
	if code := poke(a, 2, "instance"); code != w.DeliveryUnknown {
		t.Fatal("full recipient queue", code)
	}
}
