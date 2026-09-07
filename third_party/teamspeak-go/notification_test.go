package teamspeak

import (
	"testing"
	"time"

	"github.com/honeybbq/teamspeak-go/commands"
)

func TestHandleClientEnterView_AddsToClients(t *testing.T) {
	c := newTestClient(t)

	const enterCmd = "notifycliententerview clid=5 client_nickname=Alice" +
		" ctid=10 client_type=0 client_servergroups= client_unique_identifier=uid123"
	c.handleClientEnterView(commands.ParseCommand(enterCmd))

	c.mu.Lock()
	info, ok := c.clients[5]
	c.mu.Unlock()

	if !ok {
		t.Fatal("expected client 5 to be in clients map")
	}
	if info.Nickname != "Alice" {
		t.Errorf("expected nickname 'Alice', got %q", info.Nickname)
	}
	if info.ChannelID != 10 {
		t.Errorf("expected channelID 10, got %d", info.ChannelID)
	}
	if info.UID != "uid123" {
		t.Errorf("expected uid 'uid123', got %q", info.UID)
	}
}

func TestHandleClientEnterView_TriggersCallback(t *testing.T) {
	c := newTestClient(t)

	entered := make(chan ClientInfo, 1)
	c.OnClientEnter(func(ci ClientInfo) { entered <- ci })
	c.rebuildMiddlewareChains()

	const enterCmd2 = "notifycliententerview clid=5 client_nickname=Alice" +
		" ctid=10 client_type=0 client_servergroups= client_unique_identifier=uid123"
	c.handleClientEnterView(commands.ParseCommand(enterCmd2))

	select {
	case ci := <-entered:
		if ci.ID != 5 {
			t.Errorf("expected ID 5, got %d", ci.ID)
		}
	case <-time.After(time.Second):
		t.Error("OnClientEnter callback not called")
	}
}

func TestHandleClientEnterView_SetsOwnClidOnNicknameMatch(t *testing.T) {
	c := newTestClient(t)

	const enterSelf = "notifycliententerview clid=7 client_nickname=TestBot" +
		" ctid=1 client_type=0 client_servergroups= client_unique_identifier=x"
	c.handleClientEnterView(commands.ParseCommand(enterSelf))

	c.mu.Lock()
	clid := c.clid
	c.mu.Unlock()

	if clid != 7 {
		t.Errorf("expected own clid=7 after nickname match, got %d", clid)
	}
}

func TestHandleClientEnterView_InvalidClidIgnored(t *testing.T) {
	c := newTestClient(t)
	const enterZero = "notifycliententerview clid=0 client_nickname=X" +
		" ctid=1 client_type=0 client_servergroups= client_unique_identifier=x"
	c.handleClientEnterView(commands.ParseCommand(enterZero))

	c.mu.Lock()
	_, ok := c.clients[0]
	c.mu.Unlock()

	if ok {
		t.Error("client with clid=0 should be ignored")
	}
}

func TestHandleClientLeftView_RemovesFromClients(t *testing.T) {
	c := newTestClient(t)
	c.clients[5] = ClientInfo{ID: 5, Nickname: "Alice"}

	cmd := commands.ParseCommand("notifyclientleftview clid=5 reasonid=8 reasonmsg=")
	c.handleClientLeftView(cmd)

	c.mu.Lock()
	_, ok := c.clients[5]
	c.mu.Unlock()

	if ok {
		t.Error("expected client 5 to be removed")
	}
}

func TestHandleClientLeftView_KickSelf_TriggersKicked(t *testing.T) {
	c := newTestClient(t)
	c.mu.Lock()
	c.clid = 5
	c.clients[5] = ClientInfo{ID: 5}
	c.mu.Unlock()

	kicked := make(chan string, 1)
	c.OnKicked(func(msg string) { kicked <- msg })

	cmd := commands.ParseCommand("notifyclientleftview clid=5 reasonid=5 reasonmsg=banned")
	c.handleClientLeftView(cmd)

	select {
	case msg := <-kicked:
		if msg != "banned" {
			t.Errorf("expected 'banned', got %q", msg)
		}
	case <-time.After(time.Second):
		t.Error("OnKicked not called")
	}
}

func TestHandleClientLeftView_NonKickReasonid_NoKickedCallback(t *testing.T) {
	c := newTestClient(t)
	c.mu.Lock()
	c.clid = 5
	c.clients[5] = ClientInfo{ID: 5}
	c.mu.Unlock()

	kicked := make(chan string, 1)
	c.OnKicked(func(msg string) { kicked <- msg })

	// reasonid=8 = normal leave, not a kick
	cmd := commands.ParseCommand("notifyclientleftview clid=5 reasonid=8 reasonmsg=")
	c.handleClientLeftView(cmd)

	select {
	case <-kicked:
		t.Error("OnKicked should not be called for normal leave")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHandleClientMoved_UpdatesChannelID(t *testing.T) {
	c := newTestClient(t)
	c.mu.Lock()
	c.clients[3] = ClientInfo{ID: 3, ChannelID: 10}
	c.mu.Unlock()

	const moveCmd = "notifyclientmoved clid=3 ctid=20 reasonid=0" +
		" invokerid=1 invokername=Admin invokeruid=admin"
	c.handleClientMoved(commands.ParseCommand(moveCmd))

	c.mu.Lock()
	info := c.clients[3]
	c.mu.Unlock()

	if info.ChannelID != 20 {
		t.Errorf("expected channelID=20, got %d", info.ChannelID)
	}
}

func TestHandleClientMoved_TriggersCallback(t *testing.T) {
	c := newTestClient(t)
	c.mu.Lock()
	c.clients[3] = ClientInfo{ID: 3}
	c.mu.Unlock()

	moved := make(chan ClientMovedEvent, 1)
	c.OnClientMoved(func(e ClientMovedEvent) { moved <- e })
	c.rebuildMiddlewareChains()

	const moveCmd2 = "notifyclientmoved clid=3 ctid=20 reasonid=0" +
		" invokerid=1 invokername=Admin invokeruid=admin"
	c.handleClientMoved(commands.ParseCommand(moveCmd2))

	select {
	case e := <-moved:
		if e.ID != 3 || e.TargetChannelID != 20 {
			t.Errorf("unexpected event: %+v", e)
		}
	case <-time.After(time.Second):
		t.Error("OnClientMoved callback not called")
	}
}

func TestHandleTextMessage_CallsCallback(t *testing.T) {
	c := newTestClient(t)
	c.mu.Lock()
	c.clients[2] = ClientInfo{ID: 2, UID: "invokeruid"}
	c.mu.Unlock()

	msgs := make(chan TextMessage, 1)
	c.OnTextMessage(func(m TextMessage) { msgs <- m })
	c.rebuildMiddlewareChains()

	cmd := commands.ParseCommand(
		"notifytextmessage targetmode=1 target=7 invokerid=2 " +
			"invokername=Bob invokeruid=notifyuid msg=hello",
	)
	c.handleTextMessage(cmd)

	select {
	case m := <-msgs:
		if m.Message != "hello" {
			t.Errorf("expected 'hello', got %q", m.Message)
		}
		if m.TargetID != 7 {
			t.Errorf("expected TargetID 7, got %d", m.TargetID)
		}
		if m.InvokerUID != "notifyuid" {
			t.Errorf("expected InvokerUID from notify payload, got %q", m.InvokerUID)
		}
	case <-time.After(time.Second):
		t.Error("OnTextMessage not called")
	}
}

func TestHandleTextMessage_FallbackToClientCacheUID(t *testing.T) {
	c := newTestClient(t)
	c.mu.Lock()
	c.clients[99] = ClientInfo{ID: 99, UID: "cacheduid"}
	c.mu.Unlock()

	msgs := make(chan TextMessage, 1)
	c.OnTextMessage(func(m TextMessage) { msgs <- m })
	c.rebuildMiddlewareChains()

	cmd := commands.ParseCommand("notifytextmessage targetmode=2 invokerid=99 invokername=Unknown msg=hi")
	c.handleTextMessage(cmd)

	select {
	case m := <-msgs:
		if m.InvokerUID != "cacheduid" {
			t.Errorf("expected cached UID fallback, got %q", m.InvokerUID)
		}
		if m.TargetID != 0 {
			t.Errorf("expected zero TargetID when target missing, got %d", m.TargetID)
		}
	case <-time.After(time.Second):
		t.Error("OnTextMessage not called")
	}
}

func TestHandleTextMessage_MissingInvoker_NoUID(t *testing.T) {
	c := newTestClient(t)

	msgs := make(chan TextMessage, 1)
	c.OnTextMessage(func(m TextMessage) { msgs <- m })
	c.rebuildMiddlewareChains()

	cmd := commands.ParseCommand("notifytextmessage targetmode=2 invokerid=99 invokername=Unknown msg=hi")
	c.handleTextMessage(cmd)

	select {
	case m := <-msgs:
		if m.InvokerUID != "" {
			t.Errorf("expected empty UID for unknown invoker, got %q", m.InvokerUID)
		}
	case <-time.After(time.Second):
		t.Error("OnTextMessage not called")
	}
}

func TestHandleClientPoke_TriggersCallback(t *testing.T) {
	c := newTestClient(t)

	poked := make(chan PokeEvent, 1)
	c.OnPoked(func(e PokeEvent) { poked <- e })
	c.rebuildMiddlewareChains()

	cmd := commands.ParseCommand(
		"notifyclientpoke invokerid=5 invokername=Alice invokeruid=uid123 msg=hello",
	)
	c.handleClientPoke(cmd)

	select {
	case e := <-poked:
		if e.InvokerID != 5 {
			t.Errorf("expected InvokerID 5, got %d", e.InvokerID)
		}
		if e.InvokerName != "Alice" {
			t.Errorf("expected InvokerName 'Alice', got %q", e.InvokerName)
		}
		if e.InvokerUID != "uid123" {
			t.Errorf("expected InvokerUID 'uid123', got %q", e.InvokerUID)
		}
		if e.Message != "hello" {
			t.Errorf("expected Message 'hello', got %q", e.Message)
		}
	case <-time.After(time.Second):
		t.Error("OnPoked callback not called")
	}
}

func TestHandleClientPoke_EmptyMessage(t *testing.T) {
	c := newTestClient(t)

	poked := make(chan PokeEvent, 1)
	c.OnPoked(func(e PokeEvent) { poked <- e })
	c.rebuildMiddlewareChains()

	cmd := commands.ParseCommand(
		"notifyclientpoke invokerid=3 invokername=Bob invokeruid=uid456 msg=",
	)
	c.handleClientPoke(cmd)

	select {
	case e := <-poked:
		if e.Message != "" {
			t.Errorf("expected empty message, got %q", e.Message)
		}
	case <-time.After(time.Second):
		t.Error("OnPoked callback not called")
	}
}

func TestHandleNotification_DispatchesPoke(t *testing.T) {
	c := newTestClient(t)

	poked := make(chan PokeEvent, 1)
	c.OnPoked(func(e PokeEvent) { poked <- e })
	c.rebuildMiddlewareChains()

	cmd := commands.ParseCommand(
		"notifyclientpoke invokerid=5 invokername=Alice invokeruid=uid123 msg=hey",
	)
	c.handleNotification(cmd)

	select {
	case e := <-poked:
		if e.Message != "hey" {
			t.Errorf("expected 'hey', got %q", e.Message)
		}
	case <-time.After(time.Second):
		t.Error("poke not dispatched through handleNotification")
	}
}

func TestHandleNotification_UnknownNotification_NoError(t *testing.T) {
	c := newTestClient(t)
	cmd := commands.ParseCommand("notifyunknowncommand foo=bar")
	c.handleNotification(cmd)
}

func TestHandleNotification_FileTransfer_StartUpload(t *testing.T) {
	c := newTestClient(t)

	ch := make(chan any, 1)
	c.ftTrack.register()

	cmd := commands.ParseCommand("notifystartupload clientftfid=1 serverftfid=100 ftkey=abc123 port=30033 seekpos=0")
	c.handleNotification(cmd)
	close(ch)
}
