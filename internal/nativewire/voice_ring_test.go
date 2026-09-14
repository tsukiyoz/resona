package nativewire

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestVoiceRingOverwritesOldestAndReleasesReferences(t *testing.T) {
	q := NewVoiceRing(3)
	for n := byte(1); n <= 10; n++ {
		q.Push(QueuedVoice{Data: []byte{n}})
	}
	for n := byte(8); n <= 10; n++ {
		p, ok := q.pop(context.Background())
		if !ok || p.Data[0] != n {
			t.Fatalf("got %v want %d", p, n)
		}
	}
	for _, p := range q.items {
		if p.Data != nil {
			t.Fatal("consumed payload retained")
		}
	}
	q.Push(QueuedVoice{Data: []byte{11}})
	q.Close()
	q.Close()
	if q.Push(QueuedVoice{}) {
		t.Fatal("accepted after close")
	}
	if _, ok := q.pop(context.Background()); ok {
		t.Fatal("drained stale voice after close")
	}
	for _, p := range q.items {
		if p.Data != nil {
			t.Fatal("closed ring retained payload")
		}
	}
}

func TestVoiceRingConcurrentProducersAndConsumer(t *testing.T) {
	q := NewVoiceRing(4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		last := make([]uint32, 8)
		for {
			p, ok := q.pop(ctx)
			if !ok {
				return
			}
			producer, seq := int(p.Data[0]), binary.BigEndian.Uint32(p.Data[1:])
			if seq <= last[producer] {
				t.Error("duplicate or out-of-order producer data")
			}
			last[producer] = seq
		}
	}()
	var producers sync.WaitGroup
	for id := 0; id < 8; id++ {
		producers.Add(1)
		go func() {
			defer producers.Done()
			for seq := uint32(1); seq <= 1000; seq++ {
				data := make([]byte, 5)
				data[0] = byte(id)
				binary.BigEndian.PutUint32(data[1:], seq)
				q.Push(QueuedVoice{Data: data})
			}
		}()
	}
	producers.Wait()
	q.Close()
	awaitVoiceWorker(t, done)
}

func TestVoiceRingWorkerFreshnessAndStall(t *testing.T) {
	t.Run("freshness", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		q := NewVoiceRing(4)
		q.Push(QueuedVoice{Data: []byte{1}, At: time.Now().Add(-time.Second)})
		q.Push(QueuedVoice{Data: []byte{2}, At: time.Now()})
		c := &voiceTestConnection{ctx: ctx, send: func(b []byte) error {
			if len(b) != 1 || b[0] != 2 {
				t.Error("sent expired voice")
			}
			cancel()
			return nil
		}}
		done := make(chan struct{})
		go func() { defer close(done); SendVoiceRing(c, q) }()
		awaitVoiceWorker(t, done)
		if q.Push(QueuedVoice{}) {
			t.Fatal("worker left queue open")
		}
	})
	t.Run("stalled", func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		c := &voiceTestConnection{ctx: context.Background(), send: func([]byte) error { close(entered); <-release; return errors.New("closed") }, close: func() { once.Do(func() { close(release) }) }}
		q := NewVoiceRing(1)
		q.Push(QueuedVoice{Data: []byte{1}, At: time.Now()})
		done := make(chan struct{})
		go func() { defer close(done); SendVoiceRing(c, q) }()
		awaitVoiceWorker(t, entered)
		for n := 0; n < 100; n++ {
			q.Push(QueuedVoice{Data: []byte{2}, At: time.Now()})
		}
		awaitVoiceWorker(t, done)
		if c.closes.Load() != 1 {
			t.Fatal("stall watchdog did not close once")
		}
	})
}

func TestVoiceRingCloseRacesWithProducers(t *testing.T) {
	q := NewVoiceRing(4)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for range 1000 {
				q.Push(QueuedVoice{Data: []byte{1}})
			}
		}()
	}
	close(start)
	q.Close()
	workers.Wait()
	if q.size != 0 {
		t.Fatal("producer retained payload after close")
	}
	for _, p := range q.items {
		if p.Data != nil {
			t.Fatal("closed ring retained data")
		}
	}
}
