package native

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tsukiyoz/resona/internal/audio"
	"github.com/tsukiyoz/resona/internal/client"
	w "github.com/tsukiyoz/resona/internal/nativewire"
)

type resourceTestTransport struct {
	w.Connection
	closes atomic.Int32
}

func (c *resourceTestTransport) Context() context.Context            { return context.Background() }
func (c *resourceTestTransport) CloseWithError(uint64, string) error { c.closes.Add(1); return nil }

type resourceTestStream struct {
	w.Stream
	write func([]byte) (int, error)
}

func (s resourceTestStream) Write(data []byte) (int, error) { return s.write(data) }
func (resourceTestStream) SetWriteDeadline(time.Time) error { return nil }

func TestResourceTimeoutCancelAndPermanentRefusalKeepHealthyTransport(t *testing.T) {
	transport := &resourceTestTransport{}
	c := &connection{conn: transport, gate: make(chan struct{}, 1), pending: make(map[uint32]chan uint8)}
	for _, mode := range []string{"timeout", "cancel", "refused", "success"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			c.stream = resourceTestStream{write: func(data []byte) (int, error) {
				frame, err := w.Read(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				if mode == "cancel" {
					cancel()
				}
				if mode == "refused" || mode == "success" {
					code := w.OK
					if mode == "refused" {
						code = w.PermissionDenied
					}
					c.mu.Lock()
					c.pending[frame.Request] <- code
					c.mu.Unlock()
				}
				return len(data), nil
			}}
			err := c.command(ctx, w.WatchResourcesKind, w.WatchResources{})
			var failure *client.ResourceSyncError
			if mode == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("timeout=%v", err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel=%v", err)
			}
			if mode == "refused" && (!errors.As(err, &failure) || failure.Retryable) {
				t.Fatalf("refusal=%v", err)
			}
			if mode == "success" && err != nil {
				t.Fatal(err)
			}
			if transport.closes.Load() != 0 || len(c.pending) != 0 || len(c.gate) != 0 {
				t.Fatal("resource request closed transport or leaked command state")
			}
		})
	}
}

func TestResourceWatchScopesResyncAndKeepsVoiceAndChat(t *testing.T) {
	t.Run("noise", func(t *testing.T) {
		profile, stop, done := startServer(t)
		defer func() { stop(); <-done }()
		a, observed := connectTest(t, profile)
		b, _ := connectTest(t, profile)
		outside, _ := connectTest(t, profile)
		ctx := context.Background()
		if err := outside.MoveChannel(ctx, "2"); err != nil {
			t.Fatal(err)
		}
		if err := a.SetResourceInterest(ctx, true, true); err != nil {
			t.Fatal(err)
		}
		a.mu.Lock()
		full := a.state
		a.mu.Unlock()
		if len(full.Members) != 3 || len(full.Channels) != 2 {
			t.Fatal("incomplete initial list")
		}
		if err := a.SetResourceInterest(ctx, false, false); err != nil {
			t.Fatal(err)
		}
		a.mu.Lock()
		scoped := a.state
		a.mu.Unlock()
		if len(scoped.Members) != 2 || len(scoped.Channels) != 1 || scoped.AllMembers {
			t.Fatal("scope ignored")
		}
		fullBytes, _ := w.Pack(w.StateKind, 0, full)
		scopeBytes, _ := w.Pack(w.StateKind, 0, scoped)
		if len(scopeBytes) >= len(fullBytes) {
			t.Fatal("scope did not reduce bytes")
		}
		t.Logf("3 users/2 channels: full snapshot %d bytes; current-channel %d bytes", len(fullBytes), len(scopeBytes))
		if err := outside.SetVoiceMuted(ctx, false, false); err != nil {
			t.Fatal(err)
		}
		// This request is a stream barrier after the outside mutation. Only
		// its forced snapshot may advance the observer's revision.
		if err := a.SetResourceInterest(ctx, false, false); err != nil {
			t.Fatal(err)
		}
		a.mu.Lock()
		revision := a.state.Revision
		a.mu.Unlock()
		if revision != scoped.Revision+1 {
			t.Fatal("unrelated channel emitted state")
		}
		if err := b.SetVoiceMuted(ctx, false, false); err != nil {
			t.Fatal(err)
		}
		received := make(chan audio.Packet, 2)
		a.SetVoiceHandler(func(packet audio.Packet) {
			select {
			case received <- packet:
			default:
			}
		})
		if err := b.SendVoice([]byte{1, 2, 3}, audio.CodecOpusVoice); err != nil {
			t.Fatal(err)
		}
		select {
		case <-received:
		case <-time.After(time.Second):
			t.Fatal("scoping stopped voice")
		}
		if err := b.SendChannelMessage(ctx, "1", "background chat"); err != nil {
			t.Fatal(err)
		}
		eventually(t, func() bool { observed.mu.Lock(); defer observed.mu.Unlock(); return len(observed.messages) == 1 })
		if err := a.SetResourceInterest(ctx, true, true); err != nil {
			t.Fatal(err)
		}
		state := observed.snapshot()
		if len(state.Users) != 3 || len(state.Channels) != 2 || state.MemberSyncState != "ready" {
			t.Fatal("resume did not replace full scope")
		}
	})
}
