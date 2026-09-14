package broadcaster

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestCASRingNewestAndActive(t *testing.T) {
	m := &member{}
	q := newCASMemberRing(2, m)
	q.push(message{sequence: 1})
	active, ok, _ := q.pop()
	if !ok {
		t.Fatal("empty")
	}
	q.push(message{sequence: 2})
	q.push(message{sequence: 3})
	if active.sequence != 1 || m.pending.Load() != 2 || m.overwritten.Load() != 1 {
		t.Fatal("overwrote active or wrong capacity")
	}
	m.pending.Add(-1)
	q.close()
	msg, ok, _ := q.pop()
	if !ok || msg.sequence != 3 {
		t.Fatalf("not newest: %+v", msg)
	}
	m.pending.Add(-1)
	if _, ok, closed := q.pop(); ok || !closed {
		t.Fatal("did not drain")
	}
}

func TestCASRingConcurrentConsumption(t *testing.T) {
	m := &member{}
	q := newCASMemberRing(8, m)
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
	<-done
	if invalid || m.pending.Load() != 0 || delivered+m.dropped.Load() != 8000 || m.dropped.Load() != m.overwritten.Load()+m.rejected.Load() {
		t.Fatal("concurrent accounting or FIFO failure")
	}
}

func TestCASRingPausedProducerDoesNotBlockOthers(t *testing.T) {
	for _, phase := range []string{"before-publication", "after-publication"} {
		t.Run(phase, func(t *testing.T) {
			m := &member{}
			q := newCASMemberRing(4, m)
			entered, release, firstDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			hook := func(msg message) {
				if msg.sender == 0 {
					once.Do(func() { close(entered); <-release })
				}
			}
			if phase == "before-publication" {
				q.beforePublish = hook
			} else {
				q.afterPublish = hook
			}
			go func() { q.push(message{sender: 0, sequence: 1}); close(firstDone) }()
			<-entered
			othersDone := make(chan struct{})
			go func() {
				for i := 1; i <= 32; i++ {
					q.push(message{sender: 1, sequence: uint64(i)})
				}
				close(othersDone)
			}()
			select {
			case <-othersDone:
			case <-time.After(2 * time.Second):
				close(release)
				<-firstDone
				<-othersDone
				t.Fatal("paused producer blocked publication")
			}
			// Consumers can also make progress before the paused producer resumes.
			msg, ok, _ := q.pop()
			if !ok {
				close(release)
				<-firstDone
				t.Fatal("paused producer blocked consumption")
			}
			_ = msg
			m.pending.Add(-1)
			close(release)
			<-firstDone
			q.close()
			var delivered uint64 = 1
			for {
				_, ok, _ := q.pop()
				if !ok {
					break
				}
				delivered++
				m.pending.Add(-1)
			}
			if m.pending.Load() != 0 || delivered+m.dropped.Load() != 33 {
				t.Fatal("paused producer accounting")
			}
		})
	}
}
