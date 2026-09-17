package broadcaster

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemberSerialization(t *testing.T) {
	for _, model := range models {
		t.Run(model, func(t *testing.T) {
			b := newBroadcaster(model, config{Members: 4, Speakers: 4, Capacity: 512, SerialMember: true, SlowMember: -1})
			var active [4]atomic.Int32
			var violated atomic.Bool
			b.beforeConsume = func(id int) {
				if active[id].Add(1) != 1 {
					violated.Store(true)
				}
				runtime.Gosched()
				active[id].Add(-1)
			}
			var wg sync.WaitGroup
			for i := 0; i < 4; i++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					s := b.source(id)
					for j := 0; j < 50; j++ {
						s.emit(time.Now())
					}
				}(i)
			}
			wg.Wait()
			b.close()
			if violated.Load() {
				t.Fatal("overlapping member processing")
			}
			if r, err := b.result(); err != nil || r.Delivered != 600 {
				t.Fatalf("result=%+v err=%v", r, err)
			}
		})
	}
}

var models = []string{"member-queue", "room-queue", "direct", "goroutine-wait", "member-ring", "member-cas-ring"}

func TestDeliveryAndDrain(t *testing.T) {
	var checksum uint64
	for _, model := range models {
		b := newBroadcaster(model, config{Members: 10, Speakers: 4, Capacity: 512, Work: 8, SlowMember: -1})
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				s := b.source(id)
				for j := 0; j < 100; j++ {
					s.emit(time.Now())
				}
			}(i)
		}
		wg.Wait()
		b.close()
		b.close()
		r, err := b.result()
		if err != nil {
			t.Fatal(err)
		}
		if r.Offered != 3600 || r.Delivered != 3600 || r.Dropped != 0 {
			t.Fatalf("%s: %+v", model, r)
		}
		for _, m := range r.Members {
			want := uint64(400)
			if m.ID < 4 {
				want = 300
			}
			if m.Delivered != want {
				t.Fatalf("self exclusion: %+v", m)
			}
		}
		if model == models[0] {
			checksum = r.Checksum
		} else if checksum != r.Checksum {
			t.Fatal("payload mismatch")
		}
	}
}

func TestBoundedAdmission(t *testing.T) {
	for _, model := range models {
		t.Run(model, func(t *testing.T) {
			b := newBroadcaster(model, config{Members: 2, Speakers: 1, Capacity: 1, SlowMember: -1})
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			b.beforeConsume = func(int) { once.Do(func() { close(entered) }); <-release }
			s := b.source(0)
			done := make(chan struct{})
			go func() { s.emit(time.Now()); close(done) }()
			<-entered
			// A separate source handle represents a concurrent admission attempt.
			b.source(0).emit(time.Now())
			close(release)
			<-done
			b.close()
			r, err := b.result()
			if err != nil {
				t.Fatal(err)
			}
			if r.Delivered != 1 || r.Dropped != 1 || r.Members[0].Offered != 0 {
				t.Fatalf("%+v", r)
			}
		})
	}
}

func TestSlowMemberIsolation(t *testing.T) {
	b := newBroadcaster("member-queue", config{Members: 3, Speakers: 1, Capacity: 2, SlowMember: -1})
	blocked, release, fast := make(chan struct{}), make(chan struct{}), make(chan struct{})
	b.beforeConsume = func(id int) {
		if id == 1 {
			close(blocked)
			<-release
		} else {
			close(fast)
		}
	}
	b.source(0).emit(time.Now())
	<-blocked
	select {
	case <-fast:
	case <-time.After(time.Second):
		close(release)
		b.close()
		t.Fatal("fast member blocked")
	}
	close(release)
	b.close()
}
