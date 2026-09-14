package nativewire

import (
	"context"
	"sync"
)

// VoiceRing is a bounded MPSC queue with one consumer. Full queues replace the
// oldest queued voice, never the packet already owned by the send worker.
// Data must remain immutable after Push; Pop transfers that reference to the worker.
type VoiceRing struct {
	mu         sync.Mutex
	items      []QueuedVoice
	head, size int
	closed     bool
	wake       chan struct{}
}

func NewVoiceRing(capacity int) *VoiceRing {
	if capacity < 1 {
		panic("voice ring capacity must be positive")
	}
	return &VoiceRing{items: make([]QueuedVoice, capacity), wake: make(chan struct{}, 1)}
}

// Push never waits for network I/O. It returns false after Close.
func (q *VoiceRing) Push(packet QueuedVoice) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	if q.size == len(q.items) {
		q.items[q.head] = QueuedVoice{}
		q.head = (q.head + 1) % len(q.items)
		q.size--
	}
	empty := q.size == 0
	q.items[(q.head+q.size)%len(q.items)] = packet
	q.size++
	if empty {
		q.notify()
	}
	return true
}

func (q *VoiceRing) notify() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *VoiceRing) pop(ctx context.Context) (QueuedVoice, bool) {
	for {
		q.mu.Lock()
		if q.closed || ctx.Err() != nil {
			q.mu.Unlock()
			return QueuedVoice{}, false
		}
		if q.size != 0 {
			packet := q.items[q.head]
			q.items[q.head] = QueuedVoice{}
			q.head = (q.head + 1) % len(q.items)
			q.size--
			q.mu.Unlock()
			return packet, true
		}
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			return QueuedVoice{}, false
		case <-q.wake:
		}
	}
}

// Close abandons pending voice and wakes the consumer. Producers holding an old
// membership snapshot may still Push safely, but cannot retain more payloads.
func (q *VoiceRing) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	clear(q.items)
	q.size = 0
	q.notify()
}

func SendVoiceRing(c Connection, q *VoiceRing) {
	defer q.Close()
	sendVoiceSource(c, q.pop)
}
