package nativewire

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type voiceTestConnection struct {
	Connection
	ctx    context.Context
	send   func([]byte) error
	close  func()
	closes atomic.Int32
}

func (c *voiceTestConnection) Context() context.Context    { return c.ctx }
func (c *voiceTestConnection) SendDatagram(b []byte) error { return c.send(b) }
func (c *voiceTestConnection) CloseWithError(uint64, string) error {
	c.closes.Add(1)
	if c.close != nil {
		c.close()
	}
	return nil
}

func awaitVoiceWorker(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("voice worker did not exit")
	}
}

func TestVoiceQueueStaleClosedAndFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "closed", true: "failure"}[fail], func(t *testing.T) {
			var sends atomic.Int32
			c := &voiceTestConnection{ctx: context.Background(), send: func(b []byte) error {
				sends.Add(1)
				if len(b) != 1 || b[0] != 2 {
					t.Error("sent stale or zero packet")
				}
				if fail {
					return errors.New("write failed")
				}
				return nil
			}}
			q := make(chan QueuedVoice, 2)
			q <- QueuedVoice{Data: []byte{1}, At: time.Now().Add(-time.Second)}
			q <- QueuedVoice{Data: []byte{2}, At: time.Now()}
			close(q)
			done := make(chan struct{})
			go func() { defer close(done); SendVoiceQueue(c, q) }()
			awaitVoiceWorker(t, done)
			if sends.Load() != 1 {
				t.Fatalf("sends=%d", sends.Load())
			}
			want := int32(0)
			if fail {
				want = 1
			}
			if c.closes.Load() != want {
				t.Fatalf("closes=%d", c.closes.Load())
			}
		})
	}
}

func TestVoiceQueueBlockedSendTerminates(t *testing.T) {
	for _, cancelSend := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "cancel"}[cancelSend], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			c := &voiceTestConnection{ctx: ctx, send: func([]byte) error { close(entered); <-release; return errors.New("closed") }, close: func() { once.Do(func() { close(release) }) }}
			q := make(chan QueuedVoice, 1)
			q <- QueuedVoice{At: time.Now()}
			done := make(chan struct{})
			go func() { defer close(done); SendVoiceQueue(c, q) }()
			awaitVoiceWorker(t, entered)
			if cancelSend {
				cancel()
			}
			awaitVoiceWorker(t, done)
			if c.closes.Load() != 1 {
				t.Fatalf("close count=%d", c.closes.Load())
			}
		})
	}
}

func TestVoiceGuardLateCallbackAndTerminalTimeout(t *testing.T) {
	c := &voiceTestConnection{ctx: context.Background()}
	g := newVoiceSendGuard(c, time.Hour)
	defer g.stop()
	if !g.begin() || !g.end() || !g.begin() {
		t.Fatal("could not start successive writes")
	}
	// Simulate an earlier timer callback reaching the lock after the next write.
	g.expire(false)
	if c.closes.Load() != 0 {
		t.Fatal("earlier callback closed a healthy write")
	}
	g.mu.Lock()
	g.deadline = time.Now().Add(-time.Second)
	g.mu.Unlock()
	g.expire(false)
	if g.end() || g.begin() {
		t.Fatal("allowed another write after timeout")
	}
	if c.closes.Load() != 1 {
		t.Fatalf("close count=%d", c.closes.Load())
	}
	g.expire(true)
	if c.closes.Load() != 1 {
		t.Fatal("cancel repeated terminal close")
	}
}

func TestVoiceGuardDisarmsAfterCompletion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &voiceTestConnection{ctx: ctx}
	g := newVoiceSendGuard(c, time.Hour)
	if !g.begin() || !g.end() {
		t.Fatal("write failed")
	}
	g.expire(false)
	g.stop()
	cancel()
	g.expire(true)
	if c.closes.Load() != 0 {
		t.Fatal("completed worker closed connection")
	}
	if g.begin() {
		t.Fatal("stopped guard restarted")
	}
}

func TestVoiceQueueAlreadyCancelledDoesNotSend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &voiceTestConnection{ctx: ctx, send: func([]byte) error {
		t.Error("sent on cancelled connection")
		return nil
	}}
	q := make(chan QueuedVoice, 1)
	q <- QueuedVoice{Data: []byte{1}, At: time.Now()}
	done := make(chan struct{})
	go func() { defer close(done); SendVoiceQueue(c, q) }()
	awaitVoiceWorker(t, done)
}

func BenchmarkVoiceSendGuard(b *testing.B) {
	c := &voiceTestConnection{ctx: context.Background()}
	g := newVoiceSendGuard(c, time.Hour)
	defer g.stop()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		g.begin()
		g.end()
	}
}
