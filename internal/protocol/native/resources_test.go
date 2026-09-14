package native

import (
	"context"
	"testing"
	"time"

	"github.com/tsukiyoz/resona/internal/audio"
	w "github.com/tsukiyoz/resona/internal/nativewire"
)

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
