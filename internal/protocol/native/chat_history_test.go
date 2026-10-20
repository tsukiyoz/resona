package native

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tsukiyoz/resona/internal/client"
	w "github.com/tsukiyoz/resona/internal/nativewire"
	"github.com/tsukiyoz/resona/internal/server"
)

func TestChatHistorySwitchAndServerRestart(t *testing.T) {
	cfg := server.Config{Name: "chat", Password: "test-password", Chat: server.ChatConfig{Directory: filepath.Join(t.TempDir(), "chat")}, Channels: []w.Channel{{ID: 1, Name: "one"}, {ID: 2, Name: "two"}}}
	p, stop, done := startServerConfigured(t, cfg)
	a, _ := connectTest(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.SendChannelMessage(ctx, "1", "retained before join"); err != nil {
		t.Fatal(err)
	}
	p.Nickname = "B"
	b, bo := connectTest(t, p)
	eventually(t, func() bool {
		bo.mu.Lock()
		defer bo.mu.Unlock()
		return len(bo.messages) == 1 && bo.state.ChatState == "ready"
	})
	bo.mu.Lock()
	first := bo.messages[0]
	bo.mu.Unlock()
	if first.Sequence == 0 || !first.History || first.Timestamp <= 0 {
		t.Fatal(first)
	}
	if err := b.MoveChannel(ctx, "2"); err != nil {
		t.Fatal(err)
	}
	if err := a.SendChannelMessage(ctx, "1", "sent while B was away"); err != nil {
		t.Fatal(err)
	}
	if err := b.SyncChannelChat(ctx, "1"); err == nil {
		t.Fatal("cross-channel history accepted")
	}
	if err := b.MoveChannel(ctx, "1"); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		bo.mu.Lock()
		defer bo.mu.Unlock()
		return len(bo.messages) >= 3 && bo.state.ChatState == "ready"
	})
	bo.mu.Lock()
	if bo.messages[len(bo.messages)-1].Text != "sent while B was away" {
		t.Fatal(bo.messages)
	}
	bo.mu.Unlock()
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("server did not stop")
	}
	p, stop, done = startServerConfigured(t, cfg)
	_, co := connectTest(t, p)
	eventually(t, func() bool {
		co.mu.Lock()
		defer co.mu.Unlock()
		return len(co.messages) == 2 && co.state.ChatState == "ready"
	})
	co.mu.Lock()
	if co.messages[0].Sequence != first.Sequence || co.messages[0].Timestamp != first.Timestamp || co.messages[0].UserID != first.UserID {
		t.Fatal("persistent identity/order/time changed")
	}
	co.mu.Unlock()
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("restarted server did not stop")
	}
}

func TestChatOwnHistoryUsesStableIdentity(t *testing.T) {
	p, stop, done := startServer(t)
	connector := Connector{IdentityPath: filepath.Join(t.TempDir(), "identity")}
	connect := func() (*connection, *observer) {
		t.Helper()
		o := &observer{}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		remote, err := connector.Connect(ctx, p, "test-password", o.update)
		if err != nil {
			t.Fatal(err)
		}
		c := remote.(*connection)
		t.Cleanup(func() { _ = c.Close() })
		return c, o
	}
	a, ao := connect()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.SendIdentifiedChannelMessage(ctx, "1", "mine", "persistent-request"); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { ao.mu.Lock(); defer ao.mu.Unlock(); return len(ao.messages) == 1 })
	_ = a.Close()
	b, bo := connect()
	_ = b
	eventually(t, func() bool {
		bo.mu.Lock()
		defer bo.mu.Unlock()
		return len(bo.messages) == 1 && bo.state.ChatState == "ready"
	})
	bo.mu.Lock()
	m := bo.messages[0]
	bo.mu.Unlock()
	if !m.Self || !m.History || m.ClientID != "persistent-request" || m.UserID == bo.snapshot().SelfID {
		t.Fatalf("wrong stable author: %+v", m)
	}
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("shutdown stalled")
	}
}

var _ client.IdentifiedMessageSender = (*connection)(nil)
