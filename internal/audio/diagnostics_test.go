package audio

import (
	"context"
	"testing"
)

func TestVoiceDiagnosticsObserveReceiveQueueDrops(t *testing.T) {
	r := &engineRun{ctx: context.Background(), incoming: make(chan Packet, 1)}
	p := Packet{SenderID: 1, Data: []byte{1}}
	r.enqueue(p)
	r.enqueue(p)
	if r.diagnostics.received.Load() != 1 || r.diagnostics.queueDrops.Load() != 1 {
		t.Fatal("queue drops not accounted independently from received frames")
	}
}

func TestVoiceDiagnosticPeerCardinalityBounded(t *testing.T) {
	d := voiceDiagnostics{}
	for i := 1; i < 1000; i++ {
		d.peer(uint16(i))
	}
	if len(d.peers) != maxSpeakers {
		t.Fatal("peer counters are unbounded")
	}
}

func BenchmarkVoiceDiagnosticAccounting(b *testing.B) {
	d := voiceDiagnostics{}
	for i := 1; i <= maxSpeakers; i++ {
		d.peer(uint16(i))
	}
	c := voiceCounters{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.received.Add(1)
		p := d.peer(uint16(i%maxSpeakers + 1))
		p.received++
		p.decoded++
	}
}
