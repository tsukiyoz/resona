package broadcaster

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type config struct {
	Members, Speakers, Capacity, Work, Rate int
	SlowMember                              int
	SlowDelay                               time.Duration
	SerialMember                            bool
	MaxAge                                  time.Duration
}
type message struct {
	sender               int
	sequence, recipients uint64
	at                   time.Time
}
type member struct {
	processMu, statsMu                sync.Mutex
	offered, dropped                  atomic.Uint64
	rejected, overwritten, expired    atomic.Uint64
	pending                           atomic.Int32
	completed, checksum               uint64
	maxLatency                        time.Duration
	hist                              [2001]uint64
	startHist                         [2001]uint64
	startAgeTotal, completionAgeTotal time.Duration
	maxStartAge                       time.Duration
}

func (m *member) admit(limit int) bool {
	m.offered.Add(1)
	for {
		n := m.pending.Load()
		if n >= int32(limit) {
			m.dropped.Add(1)
			m.rejected.Add(1)
			return false
		}
		if m.pending.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

type broadcaster struct {
	c             config
	model         string
	members       []member
	queues        []chan message
	rings         []*memberRing
	casRings      []*casMemberRing
	wg            sync.WaitGroup
	stopOnce      sync.Once
	beforeConsume func(int)
}

func newBroadcaster(model string, c config) *broadcaster {
	if c.Members < 2 || c.Members > 64 || c.Speakers < 1 || c.Speakers > c.Members || c.Capacity < 1 || c.Work < 0 {
		panic("invalid configuration")
	}
	if model != "member-queue" && model != "room-queue" && model != "direct" && model != "goroutine-wait" && model != "member-ring" && model != "member-cas-ring" {
		panic("unknown model")
	}
	b := &broadcaster{c: c, model: model, members: make([]member, c.Members)}
	if model == "member-cas-ring" {
		for target := range b.members {
			q := newCASMemberRing(c.Capacity, &b.members[target])
			b.casRings = append(b.casRings, q)
			b.wg.Add(1)
			go func(target int) {
				defer b.wg.Done()
				for {
					msg, ok, closed := q.pop()
					if ok {
						b.consume(target, msg)
						continue
					}
					if closed {
						return
					}
					<-q.wake
				}
			}(target)
		}
		return b
	}
	if model == "member-ring" {
		for target := range b.members {
			q := newMemberRing(c.Capacity, &b.members[target])
			b.rings = append(b.rings, q)
			b.wg.Add(1)
			go func(target int) {
				defer b.wg.Done()
				for {
					msg, ok, closed := q.pop()
					if ok {
						b.consume(target, msg)
						continue
					}
					if closed {
						return
					}
					<-q.wake
				}
			}(target)
		}
		return b
	}
	if model == "direct" || model == "goroutine-wait" {
		return b
	}
	workers, capacity := c.Members, c.Capacity
	if model == "room-queue" {
		workers = 1
		capacity = c.Members * c.Capacity
	}
	for i := 0; i < workers; i++ {
		q := make(chan message, capacity)
		b.queues = append(b.queues, q)
		b.wg.Add(1)
		go func(target int) {
			defer b.wg.Done()
			for msg := range q {
				if model == "member-queue" {
					b.consume(target, msg)
					continue
				}
				start := (msg.sender + int(msg.sequence)) % c.Members
				for j := 0; j < c.Members; j++ {
					target := (start + j) % c.Members
					if msg.recipients&(uint64(1)<<target) != 0 {
						b.consume(target, msg)
					}
				}
			}
		}(i)
	}
	return b
}
func (b *broadcaster) consume(target int, msg message) {
	m := &b.members[target]
	// Queue workers already own each member. Inline callers need mutual exclusion
	// only when the workload contract requires member-local serialization.
	if b.c.SerialMember && (b.model == "direct" || b.model == "goroutine-wait") {
		m.processMu.Lock()
		defer m.processMu.Unlock()
	}
	if b.beforeConsume != nil {
		b.beforeConsume(target)
	}
	startAge := time.Since(msg.at)
	if b.c.MaxAge > 0 && startAge >= b.c.MaxAge {
		m.dropped.Add(1)
		m.expired.Add(1)
		m.pending.Add(-1)
		return
	}
	// Independent member state; no shared output lock or artificial resource.
	x := uint64(msg.sender+1)<<32 | msg.sequence
	for i := 0; i < b.c.Work; i++ {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
	}
	if target == b.c.SlowMember {
		time.Sleep(b.c.SlowDelay)
	}
	delay := time.Since(msg.at)
	// Identical short statistics lock in all models; synthetic work stays outside.
	m.statsMu.Lock()
	m.checksum += x
	m.hist[ageBucket(delay)]++
	m.startHist[ageBucket(startAge)]++
	m.startAgeTotal += startAge
	m.completionAgeTotal += delay
	m.maxStartAge = max(m.maxStartAge, startAge)
	m.maxLatency = max(m.maxLatency, delay)
	m.completed++
	m.statsMu.Unlock()
	m.pending.Add(-1)
}

type source struct {
	b        *broadcaster
	id       int
	sequence uint64
	pending  sync.WaitGroup
}

func (b *broadcaster) source(id int) *source {
	if id < 0 || id >= b.c.Speakers {
		panic("invalid speaker")
	}
	return &source{b: b, id: id}
}
func (s *source) emit(at time.Time) {
	b := s.b
	s.sequence++
	msg := message{sender: s.id, sequence: s.sequence, at: at}
	// Equal per-member credits include queued and active deliveries.
	// This common O(N) admission pass stays on the producer in both models.
	for j := 0; j < b.c.Members; j++ {
		target := (s.id + int(s.sequence) + j) % b.c.Members
		if target == s.id {
			continue
		}
		if b.model == "member-ring" {
			b.rings[target].push(msg)
			continue
		}
		if b.model == "member-cas-ring" {
			b.casRings[target].push(msg)
			continue
		}
		if !b.members[target].admit(b.c.Capacity) {
			continue
		}
		switch b.model {
		case "direct":
			b.consume(target, msg)
		case "goroutine-wait":
			s.pending.Add(1)
			go func(target int, msg message) {
				defer s.pending.Done()
				b.consume(target, msg)
			}(target, msg)
		case "member-queue":
			b.queues[target] <- msg
		case "room-queue":
			msg.recipients |= uint64(1) << target
		}
	}
	if b.model == "room-queue" && msg.recipients != 0 {
		b.queues[0] <- msg
	}
	s.pending.Wait()
}

// Call only after producers stop. All accepted work drains before return.
func (b *broadcaster) close() {
	b.stopOnce.Do(func() {
		for _, q := range b.queues {
			close(q)
		}
		for _, q := range b.rings {
			q.close()
		}
		for _, q := range b.casRings {
			q.close()
		}
		b.wg.Wait()
	})
}

type result struct {
	Model                                                    string `json:"model"`
	Scenario                                                 string `json:"scenario"`
	Round                                                    int    `json:"round"`
	Config                                                   config `json:"config"`
	Seconds, DrainMS, CPUPercent, CPUSeconds, AllocMiB       float64
	Offered, Delivered, Dropped                              uint64
	Rejected, Overwritten, Expired, RingSignals              uint64
	CASRetries                                               uint64
	P50MS, P99MS, P999MS                                     *float64
	MaxMS, MaxInputLagMS, MinDeliveryRatio, MaxDeliveryRatio float64
	Checksum                                                 uint64
	Members                                                  []memberResult
	StartP99MS                                               *float64
	MeanStartMS, MeanCompletionMS, MaxStartMS                float64
}
type memberResult struct {
	ID                             int
	Offered, Delivered, Dropped    uint64
	P99MS                          *float64
	Rejected, Overwritten, Expired uint64
	StartP99MS                     *float64
	MeanStartMS, MeanCompletionMS  float64
}

// 100-us buckets through 200 ms; preserve exact means and maxima beyond it.
func ageBucket(age time.Duration) int { return min(max(int(age/(100*time.Microsecond)), 0), 2000) }

func quantile(hist [2001]uint64, count uint64, p float64) *float64 {
	var sum uint64
	for i, n := range hist {
		sum += n
		if count > 0 && float64(sum) >= float64(count)*p {
			if i == 2000 {
				return nil
			}
			v := float64(i+1) / 10
			return &v
		}
	}
	return nil
}
func (b *broadcaster) result() (result, error) {
	r := result{Model: b.model, Config: b.c, MinDeliveryRatio: 1}
	var hist [2001]uint64
	var startHist [2001]uint64
	var startTotal, completionTotal time.Duration
	for i := range b.members {
		m := &b.members[i]
		offered, dropped := m.offered.Load(), m.dropped.Load()
		if m.pending.Load() != 0 || offered != m.completed+dropped {
			return r, fmt.Errorf("member %d: accounting mismatch", i)
		}
		r.Offered += offered
		r.Delivered += m.completed
		r.Dropped += dropped
		r.Rejected += m.rejected.Load()
		r.Overwritten += m.overwritten.Load()
		r.Expired += m.expired.Load()
		if dropped != m.rejected.Load()+m.overwritten.Load()+m.expired.Load() {
			return r, fmt.Errorf("member %d: drop reasons mismatch", i)
		}
		r.Checksum += m.checksum
		if offered > 0 {
			ratio := float64(m.completed) / float64(offered)
			r.MinDeliveryRatio = min(r.MinDeliveryRatio, ratio)
			r.MaxDeliveryRatio = max(r.MaxDeliveryRatio, ratio)
		}
		r.MaxMS = max(r.MaxMS, float64(m.maxLatency)/float64(time.Millisecond))
		for j, n := range m.hist {
			hist[j] += n
			startHist[j] += m.startHist[j]
		}
		mr := memberResult{ID: i, Offered: offered, Delivered: m.completed, Dropped: dropped, P99MS: quantile(m.hist, m.completed, .99), Rejected: m.rejected.Load(), Overwritten: m.overwritten.Load(), Expired: m.expired.Load(), StartP99MS: quantile(m.startHist, m.completed, .99)}
		if m.completed > 0 {
			mr.MeanStartMS = float64(m.startAgeTotal) / 1e6 / float64(m.completed)
			mr.MeanCompletionMS = float64(m.completionAgeTotal) / 1e6 / float64(m.completed)
		}
		r.Members = append(r.Members, mr)
		startTotal += m.startAgeTotal
		completionTotal += m.completionAgeTotal
		r.MaxStartMS = max(r.MaxStartMS, float64(m.maxStartAge)/1e6)
	}
	r.P50MS = quantile(hist, r.Delivered, .5)
	r.P99MS = quantile(hist, r.Delivered, .99)
	r.P999MS = quantile(hist, r.Delivered, .999)
	r.StartP99MS = quantile(startHist, r.Delivered, .99)
	if r.Delivered > 0 {
		r.MeanStartMS = float64(startTotal) / 1e6 / float64(r.Delivered)
		r.MeanCompletionMS = float64(completionTotal) / 1e6 / float64(r.Delivered)
	}
	for _, q := range b.rings {
		r.RingSignals += q.signals
	}
	for _, q := range b.casRings {
		r.RingSignals += q.signals.Load()
		r.CASRetries += q.retries.Load()
	}
	return r, nil
}
