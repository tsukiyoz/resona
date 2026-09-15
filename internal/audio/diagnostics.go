package audio

import (
	"log/slog"
	"sync/atomic"
	"time"
)

type voiceCounters struct {
	received, queueDrops, sent, sendErrors, captureDrops atomic.Uint64
}

// RecordDiagnostics samples only atomics, on explicit export. Per-peer interval
// counters remain exclusively owned by mixLoop.
func (e *Engine) RecordDiagnostics() {
	e.mu.RLock()
	r := e.run
	e.mu.RUnlock()
	if r == nil {
		return
	}
	c := &r.diagnostics
	slog.Info("voice counters", "received", c.received.Load(), "receive_queue_drops", c.queueDrops.Load(), "sent", c.sent.Load(), "send_errors", c.sendErrors.Load(), "capture_dropped_samples", c.captureDrops.Load(), "final", false)
}

type peerCounters struct {
	received, decoded, decodeErrors, jitterDrops, unknownPeer, mutedFrames uint64
}

// Owned by mixLoop. Fixed cardinality and an existing audio tick: no extra
// goroutine, per-packet log, PCM capture, or desktop notification.
type voiceDiagnostics struct {
	peers    map[uint16]*peerCounters
	pool     [maxSpeakers]peerCounters
	last     time.Time
	previous [5]uint64
}

func (d *voiceDiagnostics) peer(id uint16) *peerCounters {
	if d.peers == nil {
		d.peers = make(map[uint16]*peerCounters, maxSpeakers)
	}
	if p := d.peers[id]; p != nil {
		return p
	}
	if len(d.peers) >= maxSpeakers {
		return nil
	}
	p := &d.pool[len(d.peers)]
	*p = peerCounters{}
	d.peers[id] = p
	return p
}

func (d *voiceDiagnostics) report(c *voiceCounters, now time.Time, final bool) {
	if !final && now.Sub(d.last) < 30*time.Second {
		return
	}
	d.last = now
	values := [5]uint64{c.received.Load(), c.queueDrops.Load(), c.sent.Load(), c.sendErrors.Load(), c.captureDrops.Load()}
	if values != d.previous {
		slog.Info("voice counters", "received", values[0], "receive_queue_drops", values[1], "sent", values[2], "send_errors", values[3], "capture_dropped_samples", values[4], "final", final)
		d.previous = values
	}
	for id, p := range d.peers {
		if *p != (peerCounters{}) {
			slog.Info("voice peer interval", "sender", id, "received", p.received, "decoded", p.decoded, "decode_errors", p.decodeErrors, "jitter_rejected", p.jitterDrops, "unknown_peer", p.unknownPeer, "muted_frames", p.mutedFrames)
		}
	}
	clear(d.peers)
}
