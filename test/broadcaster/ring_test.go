package broadcaster

import "sync"

// MPSC queue with a single consumer. Capacity includes its active delivery.
// Messages are values: overwriting a queued slot cannot mutate a popped message.
type memberRing struct {
	mu         sync.Mutex
	items      []message
	head, size int
	closed     bool
	wake       chan struct{}
	member     *member
	signals    uint64
}

func newMemberRing(capacity int, m *member) *memberRing {
	return &memberRing{items: make([]message, capacity), wake: make(chan struct{}, 1), member: m}
}

func (q *memberRing) notify() {
	select {
	case q.wake <- struct{}{}:
		q.signals++
	default:
	}
}

func (q *memberRing) push(msg message) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		panic("push after ring closed")
	}
	m := q.member
	m.offered.Add(1)
	if m.pending.Load() >= int32(len(q.items)) {
		if q.size == 0 {
			// Capacity can be entirely occupied by an active delivery (capacity=1).
			m.dropped.Add(1)
			m.rejected.Add(1)
			return
		}
		q.items[q.head] = message{}
		q.head = (q.head + 1) % len(q.items)
		q.size--
		m.dropped.Add(1)
		m.overwritten.Add(1)
		// Reuse the evicted message's pending credit for the incoming message.
	} else {
		m.pending.Add(1)
	}
	empty := q.size == 0
	q.items[(q.head+q.size)%len(q.items)] = msg
	q.size++
	if empty {
		q.notify()
	}
}

func (q *memberRing) pop() (message, bool, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.size == 0 {
		return message{}, false, q.closed
	}
	msg := q.items[q.head]
	q.items[q.head] = message{}
	q.head = (q.head + 1) % len(q.items)
	q.size--
	return msg, true, q.closed
}

func (q *memberRing) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.notify()
}
