package broadcaster

import "sync/atomic"

// Payload and turn are immutable after publication; claimed never resets.
// GC retains entries referenced by readers. Do not pool/reuse them in place.
type casEntry struct {
	turn    uint64
	msg     message
	claimed atomic.Bool
}
type paddedCursor struct {
	value atomic.Uint64
	_     [15]uint64
}

// Publish before advancing tail. Others help lagging cursors, never wait for
// reserved slots. Producer eviction shares the consumer's CAS claim operation.
type casMemberRing struct {
	head, tail                  paddedCursor
	slots                       []atomic.Pointer[casEntry]
	member                      *member
	closed                      atomic.Bool
	wake                        chan struct{}
	signals, retries            atomic.Uint64
	beforePublish, afterPublish func(message)
}

func newCASMemberRing(capacity int, m *member) *casMemberRing {
	return &casMemberRing{slots: make([]atomic.Pointer[casEntry], capacity), member: m, wake: make(chan struct{}, 1)}
}

// Core uses loads/CAS and payload copies. Allocation, statistics and notification
// are separate and do not inherit the core's nonblocking progress guarantee.
func (q *casMemberRing) publish(entry *casEntry) {
	for {
		tail := q.tail.value.Load()
		if tail == ^uint64(0) {
			panic("CAS ring generation exhausted")
		}
		slot := &q.slots[tail%uint64(len(q.slots))]
		old := slot.Load()
		if old != nil && old.turn >= tail {
			q.tail.value.CompareAndSwap(tail, tail+1)
			continue
		}
		if old != nil && !old.claimed.Load() {
			panic("reserved delivery has no free CAS ring slot")
		}
		entry.turn = tail
		if q.beforePublish != nil {
			q.beforePublish(entry.msg)
		}
		if !slot.CompareAndSwap(old, entry) {
			q.retries.Add(1)
			continue
		}
		if q.afterPublish != nil {
			q.afterPublish(entry.msg)
		}
		q.tail.value.CompareAndSwap(tail, tail+1)
		return
	}
}
func (q *casMemberRing) take() (message, bool) {
	for {
		head := q.head.value.Load()
		entry := q.slots[head%uint64(len(q.slots))].Load()
		if entry == nil || entry.turn < head {
			return message{}, false
		}
		if entry.turn > head || entry.claimed.Load() {
			q.head.value.CompareAndSwap(head, head+1)
			continue
		}
		if !entry.claimed.CompareAndSwap(false, true) {
			q.retries.Add(1)
			continue
		}
		q.head.value.CompareAndSwap(head, head+1)
		return entry.msg, true
	}
}
func (q *casMemberRing) push(msg message) {
	if q.closed.Load() {
		panic("push after CAS ring closed")
	}
	m := q.member
	m.offered.Add(1)
	for {
		pending := m.pending.Load()
		if pending < int32(len(q.slots)) {
			if m.pending.CompareAndSwap(pending, pending+1) {
				break
			}
			q.retries.Add(1)
			continue
		}
		if _, ok := q.take(); ok {
			m.dropped.Add(1)
			m.overwritten.Add(1)
			break
		}
		// Credits are all active or held by not-yet-published producers.
		m.dropped.Add(1)
		m.rejected.Add(1)
		return
	}
	q.publish(&casEntry{msg: msg})
	q.notify()
}
func (q *casMemberRing) notify() {
	select {
	case q.wake <- struct{}{}:
		q.signals.Add(1)
	default:
	}
}
func (q *casMemberRing) pop() (message, bool, bool) {
	msg, ok := q.take()
	return msg, ok, q.closed.Load()
}
func (q *casMemberRing) close() { q.closed.Store(true); q.notify() }
