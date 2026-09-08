package audio

import "testing"

func TestJitterOrdersPacketsAndWrapsSequence(t *testing.T) {
	for _, sequences := range [][]uint16{{10, 12, 11}, {65535, 1, 0}} {
		jitter := newJitterBuffer()
		for _, sequence := range sequences {
			if !jitter.Push(Packet{Sequence: sequence, Data: []byte{1}}) {
				t.Fatalf("rejected sequence %d", sequence)
			}
		}
		want := sequences[0]
		for range 3 {
			packet, started, lost := jitter.Pop()
			if !started || lost || packet.Sequence != want {
				t.Fatalf("pop = %+v started=%v lost=%v, want %d", packet, started, lost, want)
			}
			want++
		}
	}
}

func TestJitterIsBoundedAndRejectsStalePackets(t *testing.T) {
	jitter := newJitterBuffer()
	if !jitter.Push(Packet{Sequence: 20, Data: []byte{1}}) {
		t.Fatal("first packet rejected")
	}
	if jitter.Push(Packet{Sequence: 19, Data: []byte{1}}) || jitter.Push(Packet{Sequence: 20, Data: []byte{1}}) {
		t.Fatal("stale or duplicate packet accepted")
	}
	for i := 1; i < jitterMaxPackets; i++ {
		jitter.Push(Packet{Sequence: uint16(20 + i), Data: []byte{1}})
	}
	if jitter.Push(Packet{Sequence: 100, Data: []byte{1}}) {
		t.Fatal("overflow packet accepted")
	}
}

func TestJitterEndFlushesShortBurstInSequenceAndRejectsStaleEnd(t *testing.T) {
	jitter := newJitterBuffer()
	if !jitter.Push(Packet{Sequence: 10, Data: []byte{1}}) || !jitter.Push(Packet{Sequence: 12, End: true}) || !jitter.Push(Packet{Sequence: 11, Data: []byte{2}}) {
		t.Fatal("short burst packets were rejected")
	}
	for _, want := range []uint16{10, 11, 12} {
		packet, started, lost := jitter.Pop()
		if !started || lost || packet.Sequence != want {
			t.Fatalf("pop = %+v started=%v lost=%v, want %d", packet, started, lost, want)
		}
	}
	if jitter.Push(Packet{Sequence: 9, End: true}) {
		t.Fatal("stale end packet interrupted a newer stream")
	}
}
