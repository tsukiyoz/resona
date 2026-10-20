package server

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	w "github.com/tsukiyoz/resona/internal/nativewire"
)

type chatTestConnection struct {
	w.Connection
	ctx    context.Context
	closed atomic.Bool
}

func (c *chatTestConnection) Context() context.Context { return c.ctx }
func (c *chatTestConnection) CloseWithError(uint64, string) error {
	c.closed.Store(true)
	return nil
}

type chatTestStream struct {
	w.Stream
	frames chan []byte
}

func (s chatTestStream) SetWriteDeadline(time.Time) error { return nil }
func (s chatTestStream) Write(data []byte) (int, error) {
	s.frames <- append([]byte(nil), data...)
	return len(data), nil
}

func TestChatBackpressureKeepsCriticalQueueAndCancels(t *testing.T) {
	c := testChat(t, ChatConfig{})
	for i := range 10 {
		publishChat(t, c, 1, i, "retained")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn := &chatTestConnection{ctx: ctx}
	p := &peer{conn: conn, member: w.Member{ID: 1, Channel: 1}, epoch: 1, out: make(chan []byte, 16), chatOut: make(chan []byte, 4), chatWake: make(chan struct{}, 1)}
	s := &Server{chat: c, peers: map[uint16]*peer{1: p}}
	s.restartChatLocked(p)
	done := make(chan struct{})
	go func() { s.chatLoop(p); close(done) }()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for len(p.chatOut) < cap(p.chatOut) {
		select {
		case <-deadline.C:
			t.Fatal("history did not reach backpressure")
		case <-time.After(time.Millisecond):
		}
	}
	s.mu.Lock()
	for range 10 {
		s.enqueueLocked(p, w.ReplyKind, 1, w.Reply{})
	}
	s.mu.Unlock()
	if conn.closed.Load() || len(p.out) != 11 {
		t.Fatal("history consumed critical control capacity")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("backpressured history did not cancel")
	}
}

func TestChatWriterPrioritizesWelcomeAndCriticalControl(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := chatTestStream{frames: make(chan []byte, 4)}
	p := &peer{conn: &chatTestConnection{ctx: ctx}, stream: stream, out: make(chan []byte, 2), chatOut: make(chan []byte, 2), done: make(chan struct{})}
	msg, _ := w.Pack(w.MessageKind, 0, w.Message{Text: "history"})
	p.chatOut <- msg
	s := &Server{}
	s.enqueueLocked(p, w.WelcomeKind, 0, w.State{})
	s.enqueueLocked(p, w.ReplyKind, 1, w.Reply{})
	done := make(chan struct{})
	go func() { s.writeLoop(p); close(done) }()
	defer func() { cancel(); <-done }()
	for _, kind := range []uint8{w.WelcomeKind, w.ReplyKind, w.MessageKind} {
		select {
		case data := <-stream.frames:
			frame, err := w.Read(bytes.NewReader(data))
			if err != nil || frame.Kind != kind {
				t.Fatalf("control priority changed: got %d want %d (%v)", frame.Kind, kind, err)
			}
		case <-time.After(time.Second):
			t.Fatal("writer stalled")
		}
	}
}

func testChat(t *testing.T, cfg ChatConfig) *chatStore {
	t.Helper()
	c, err := openChat(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.close)
	return c
}

func publishChat(t *testing.T, c *chatStore, channel uint16, i int, text string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.publish(ctx, w.Message{Channel: channel, Sender: 1, AuthorUID: strings.Repeat("a", 64), Nickname: "A", Text: text, ClientID: fmt.Sprintf("message-%08d", i)}); err != nil {
		t.Fatal(err)
	}
}

func readChat(t *testing.T, c *chatStore, channel uint16, count int) []w.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var got []w.Message
	_ = c.read(ctx, channel, func(msg w.Message) error {
		got = append(got, msg)
		if len(got) == count {
			cancel()
		}
		return nil
	}, func() error { return nil })
	if len(got) != count {
		t.Fatalf("got %d messages, want %d", len(got), count)
	}
	return got
}

func TestChatRetentionAndRestart(t *testing.T) {
	cfg := ChatConfig{Directory: t.TempDir()}
	c, err := openChat(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 201; i++ {
		publishChat(t, c, 1, i, fmt.Sprint(i))
	}
	publishChat(t, c, 2, 1000, "other channel")
	c.close()
	c = testChat(t, cfg)
	got := readChat(t, c, 1, 200)
	if got[0].Text != "1" || got[199].Text != "200" || got[0].Timestamp <= 0 || !got[0].History || got[0].AuthorUID != strings.Repeat("a", 64) {
		t.Fatalf("retained history mismatch: first=%+v last=%+v", got[0], got[199])
	}
	for i := 1; i < len(got); i++ {
		if got[i].Sequence <= got[i-1].Sequence {
			t.Fatal("history order changed")
		}
	}
	if other := readChat(t, c, 2, 1); other[0].Text != "other channel" {
		t.Fatal(other)
	}
}

func TestChatGlobalByteLimitAndAge(t *testing.T) {
	c := testChat(t, ChatConfig{MaxBytes: 16 << 10, MaxAge: time.Second})
	for i := 0; i < 5; i++ {
		publishChat(t, c, uint16(1+i%2), i, strings.Repeat("x", 7000))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	info, err := c.stream.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Bytes > 16<<10 || info.State.Msgs >= 5 || info.State.FirstSeq <= 1 {
		t.Fatalf("global byte retention: %+v", info.State)
	}
	for ctx.Err() == nil {
		info, err = c.stream.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if info.State.Msgs == 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("expired chat messages were not removed")
}

func TestChatRetentionCanShrinkAfterRestart(t *testing.T) {
	cfg := ChatConfig{Directory: t.TempDir()}
	c, err := openChat(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		publishChat(t, c, 1, i, strings.Repeat("x", 7000))
	}
	c.close()
	cfg.MaxBytes = 16 << 10
	cfg.MaxMessagesPerChannel = 2
	c = testChat(t, cfg)
	info, err := c.stream.Info(context.Background())
	if err != nil || info.State.Bytes > uint64(cfg.MaxBytes) || info.State.Msgs != 2 {
		t.Fatalf("updated retention not enforced: %+v (%v)", info, err)
	}
}

func TestChatCatchupContinuesLiveWithoutDuplicates(t *testing.T) {
	c := testChat(t, ChatConfig{})
	for i := 0; i < 8; i++ {
		publishChat(t, c, 1, i, "same text")
	}
	// Reusing a client request ID within the explicit deduplication window.
	publishChat(t, c, 1, 7, "same text")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	ready := make(chan struct{}, 1)
	done := make(chan []w.Message, 1)
	go func() {
		var got []w.Message
		_ = c.read(ctx, 1, func(msg w.Message) error {
			got = append(got, msg)
			if len(got) == 9 {
				cancel()
			}
			return nil
		}, func() error { ready <- struct{}{}; return nil })
		done <- got
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("history did not complete")
	}
	publishChat(t, c, 1, 8, "live")
	got := <-done
	if len(got) != 9 || got[8].Text != "live" || got[8].History {
		t.Fatalf("catch-up/live mismatch: %+v", got)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Sequence <= got[i-1].Sequence {
			t.Fatal("duplicate or unordered message")
		}
	}
	info, err := c.stream.Info(context.Background())
	if err != nil || info.State.Consumers != 0 {
		t.Fatalf("consumer leaked after cancellation: %v %+v", err, info)
	}
}
