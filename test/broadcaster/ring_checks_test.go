package broadcaster

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestRingConcurrentConsumption(t *testing.T) {
	m := &member{}
	q := newMemberRing(8, m)
	done := make(chan struct{})
	var delivered uint64
	var last [8]uint64
	var invalid bool
	go func() {
		defer close(done)
		for {
			msg, ok, closed := q.pop()
			if !ok {
				if closed {
					return
				}
				<-q.wake
				continue
			}
			if msg.sequence <= last[msg.sender] {
				invalid = true
			}
			last[msg.sender] = msg.sequence
			runtime.Gosched()
			delivered++
			m.pending.Add(-1)
		}
	}()
	var producers sync.WaitGroup
	for i := 0; i < 8; i++ {
		producers.Add(1)
		go func(id int) {
			defer producers.Done()
			for j := 1; j <= 1000; j++ {
				q.push(message{sender: id, sequence: uint64(j)})
			}
		}(i)
	}
	producers.Wait()
	q.close()
	<-done
	if invalid || m.pending.Load() != 0 || delivered+m.dropped.Load() != 8000 || m.dropped.Load() != m.overwritten.Load()+m.rejected.Load() {
		t.Fatal("concurrent consumption lost, duplicated or reordered data")
	}
}

func TestRingKeepsNewestAndDoesNotOverwriteActive(t *testing.T) {
	m := &member{}
	q := newMemberRing(2, m)
	q.push(message{sequence: 1})
	active, ok, _ := q.pop()
	if !ok {
		t.Fatal("missing first item")
	}
	q.push(message{sequence: 2})
	q.push(message{sequence: 3})
	if active.sequence != 1 {
		t.Fatal("active delivery was overwritten")
	}
	if m.pending.Load() != 2 || m.overwritten.Load() != 1 || m.rejected.Load() != 0 {
		t.Fatal("wrong capacity accounting")
	}
	m.pending.Add(-1)
	q.close()
	latest, ok, closed := q.pop()
	if !ok || !closed || latest.sequence != 3 {
		t.Fatalf("latest=%+v ok=%v closed=%v", latest, ok, closed)
	}
	m.pending.Add(-1)
	if _, ok, closed := q.pop(); ok || !closed {
		t.Fatal("closed queue did not drain")
	}
}

func TestRingFIFOAndNotification(t *testing.T) {
	q := newMemberRing(3, &member{})
	for i := uint64(1); i <= 5; i++ {
		q.push(message{sequence: i})
	}
	if len(q.wake) != 1 || q.signals != 1 {
		t.Fatal("notifications not coalesced")
	}
	<-q.wake
	for i := uint64(3); i <= 5; i++ {
		msg, ok, _ := q.pop()
		if !ok || msg.sequence != i {
			t.Fatalf("want %d got %+v", i, msg)
		}
		q.member.pending.Add(-1)
	}
	q.push(message{sequence: 6})
	if len(q.wake) != 1 {
		t.Fatal("empty-to-nonempty did not wake")
	}
}

func TestRingConcurrentOverwrite(t *testing.T) {
	m := &member{}
	q := newMemberRing(64, m)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 1; j <= 1000; j++ {
				q.push(message{sender: id, sequence: uint64(j)})
			}
		}(i)
	}
	wg.Wait()
	q.close()
	seen := make(map[[2]uint64]bool)
	for {
		msg, ok, _ := q.pop()
		if !ok {
			break
		}
		key := [2]uint64{uint64(msg.sender), msg.sequence}
		if seen[key] {
			t.Fatal("duplicate delivery")
		}
		seen[key] = true
		m.pending.Add(-1)
	}
	if len(seen) != 64 || m.pending.Load() != 0 || m.offered.Load() != 8000 || m.overwritten.Load() != 7936 {
		t.Fatal("concurrent overwrite accounting")
	}
}

func TestExpiredMessagesAreNotProcessed(t *testing.T) {
	for _, model := range models {
		t.Run(model, func(t *testing.T) {
			b := newBroadcaster(model, config{Members: 2, Speakers: 1, Capacity: 4, MaxAge: time.Millisecond, SlowMember: -1})
			b.source(0).emit(time.Now().Add(-time.Second))
			b.close()
			r, err := b.result()
			if err != nil || r.Offered != 1 || r.Delivered != 0 || r.Expired != 1 {
				t.Fatalf("result=%+v err=%v", r, err)
			}
		})
	}
}
