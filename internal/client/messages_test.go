package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeMessageConnection struct {
	fakeConnection
	send func(context.Context, string, string) error
}

func TestTranscriptByteLimitPreservesPendingSend(t *testing.T) {
	s := &Service{}
	s.state.Session.SendingMessageID = "pending"
	s.state.Messages = []Message{{ID: "pending", Text: "not acknowledged"}}
	for i := 0; i < 140; i++ {
		s.state.Messages = append(s.state.Messages, Message{ID: fmt.Sprint(i), Text: strings.Repeat("x", 4096)})
	}
	s.trimMessagesLocked()
	total := 0
	for _, message := range s.state.Messages {
		total += len(message.Text) + len(message.Author) + len(message.Error)
	}
	if total > maxMessageBytes || len(s.state.Messages) > maxMessages || s.state.Messages[0].ID != "pending" || s.state.Messages[len(s.state.Messages)-1].ID != "139" {
		t.Fatalf("invalid transcript eviction: bytes=%d count=%d", total, len(s.state.Messages))
	}
}

func (c *fakeMessageConnection) SendChannelMessage(ctx context.Context, channelID, text string) error {
	if c.send == nil {
		return ErrMessageUnavailable
	}
	return c.send(ctx, channelID, text)
}

func connectedMessageService(t *testing.T, connection *fakeMessageConnection) (*Service, func(RemoteState)) {
	t.Helper()
	updates := make(chan func(RemoteState), 1)
	service, profile := serviceWithProfile(t, connectorFunc(func(_ context.Context, _ ServerProfile, _ string, update func(RemoteState)) (RemoteConnection, error) {
		updates <- update
		update(channelState("1"))
		return connection, nil
	}))
	if _, err := service.ConnectServer(profile.ID, ""); err != nil {
		t.Fatal(err)
	}
	waitForMode(t, service, "connected")
	t.Cleanup(service.Shutdown)
	return service, <-updates
}

func waitForMessageResult(t *testing.T, service *Service, id, status string) Workspace {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state, _ := service.GetWorkspace()
		for _, message := range state.Messages {
			if message.ID == id && message.Status == status && state.Session.SendingMessageID == "" {
				return state
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("message did not reach status %q", status)
	return Workspace{}
}

func sendTestMessage(t *testing.T, service *Service, text string) Workspace {
	t.Helper()
	state, _ := service.GetWorkspace()
	state, err := service.SendChannelMessage(state.Session.ID, state.Session.ChannelID, text)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestChannelMessagePendingAndDeliveryResults(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		err          error
	}{
		{"acknowledged", "sent", nil},
		{"permission", "failed", ErrMessagePermissionDenied},
		{"explicit rejection", "failed", &MessageSendError{Message: "服务器拒绝了消息"}},
		{"unknown delivery", "unconfirmed", &MessageSendError{Message: "未收到发送确认", Uncertain: true}},
		{"timeout", "unconfirmed", context.DeadlineExceeded},
		{"unknown error", "unconfirmed", errors.New("private transport detail")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := make(chan error, 1)
			sent := make(chan string, 1)
			connection := &fakeMessageConnection{send: func(ctx context.Context, channelID, text string) error {
				sent <- channelID + ":" + text
				select {
				case err := <-result:
					return err
				case <-ctx.Done():
					return ctx.Err()
				}
			}}
			service, update := connectedMessageService(t, connection)
			pending := sendTestMessage(t, service, "  [b]hello[/b]\n中文  ")
			if len(pending.Messages) != 1 || pending.Messages[0].Status != "sending" || pending.Messages[0].ID != pending.Session.SendingMessageID {
				t.Fatalf("missing pending message: %+v", pending)
			}
			id := pending.Messages[0].ID
			if got := <-sent; got != "1:  [b]hello[/b]\n中文  " {
				t.Fatalf("text changed before sending: %q", got)
			}
			update(channelState("1"))
			result <- tc.err
			state := waitForMessageResult(t, service, id, tc.status)
			message := state.Messages[0]
			if message.ID != id || message.AuthorID != "self" || message.Author != "Alice" || message.CreatedAt == "" || strings.Contains(message.Error, "private") {
				t.Fatalf("bad completed message: %+v", message)
			}
			if (tc.err == nil) != (message.Error == "") {
				t.Fatalf("completion error does not match result: %+v", message)
			}
		})
	}
}

func TestChannelMessageRejectsStaleDestinationAndInvalidText(t *testing.T) {
	var sends atomic.Int32
	service, update := connectedMessageService(t, &fakeMessageConnection{send: func(context.Context, string, string) error { sends.Add(1); return nil }})
	state, _ := service.GetWorkspace()
	for _, tc := range []struct{ session, channel, text string }{
		{"", "1", "message"}, {"old-session", "1", "message"}, {state.Session.ID, "2", "message"},
		{state.Session.ID, "1", " \n\t "}, {state.Session.ID, "1", "a\x00b"},
		{state.Session.ID, "1", string([]byte{0xff})}, {state.Session.ID, "1", strings.Repeat("中", MaxChannelMessageBytes/3+1)},
	} {
		if _, err := service.SendChannelMessage(tc.session, tc.channel, tc.text); err == nil {
			t.Fatal("invalid message request accepted")
		}
	}
	update(channelState("2"))
	if _, err := service.SendChannelMessage(state.Session.ID, "1", "stale channel"); !errors.Is(err, ErrMessageChannelChanged) {
		t.Fatalf("old-channel message was not rejected: %v", err)
	}
	state, _ = service.GetWorkspace()
	if sends.Load() != 0 || len(state.Messages) != 0 {
		t.Fatal("rejected requests produced sends or local message rows")
	}
	unsupported, _ := connectedChannelService(t, &fakeConnection{})
	state, _ = unsupported.GetWorkspace()
	if _, err := unsupported.SendChannelMessage(state.Session.ID, "1", "message"); !errors.Is(err, ErrMessageUnavailable) {
		t.Fatalf("connection without optional sender accepted a message: %v", err)
	}
}

func TestChannelMessageAndMoveAreSingleFlight(t *testing.T) {
	sendStarted, sendAck := make(chan struct{}), make(chan struct{})
	moveStarted := make(chan struct{})
	connection := &fakeMessageConnection{
		fakeConnection: fakeConnection{move: func(ctx context.Context, _ string) error { close(moveStarted); <-ctx.Done(); return ctx.Err() }},
		send: func(ctx context.Context, _, _ string) error {
			close(sendStarted)
			select {
			case <-sendAck:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	service, _ := connectedMessageService(t, connection)
	pending := sendTestMessage(t, service, "first")
	<-sendStarted
	if _, err := service.SendChannelMessage(pending.Session.ID, "1", "duplicate"); err == nil {
		t.Fatal("second send accepted while first was pending")
	}
	if _, err := service.SelectChannel("2"); err == nil {
		t.Fatal("move accepted while a message was pending")
	}
	if _, err := service.RetryMessage(pending.Messages[0].ID, true); err == nil {
		t.Fatal("pending message retried")
	}
	close(sendAck)
	waitForMessageResult(t, service, pending.Messages[0].ID, "sent")
	if _, err := service.SelectChannel("2"); err != nil {
		t.Fatal(err)
	}
	<-moveStarted
	if _, err := service.SendChannelMessage(pending.Session.ID, "1", "during move"); err == nil {
		t.Fatal("message accepted during a pending move")
	}
}

func TestChannelMessageRetryRequiresConfirmationWithoutDuplicatingLocalRow(t *testing.T) {
	var calls atomic.Int32
	service, _ := connectedMessageService(t, &fakeMessageConnection{send: func(context.Context, string, string) error {
		if calls.Add(1) == 1 {
			return ErrMessageDeliveryUnknown
		}
		return nil
	}})
	pending := sendTestMessage(t, service, "retry me")
	id, created := pending.Messages[0].ID, pending.Messages[0].CreatedAt
	waitForMessageResult(t, service, id, "unconfirmed")
	if _, err := service.RetryMessage(id, false); err == nil || calls.Load() != 1 {
		t.Fatal("unknown delivery retried without confirmation")
	}
	if _, err := service.RetryMessage(id, true); err != nil {
		t.Fatal(err)
	}
	state := waitForMessageResult(t, service, id, "sent")
	if len(state.Messages) != 1 || state.Messages[0].CreatedAt != created || calls.Load() != 2 {
		t.Fatalf("retry created a duplicate local row or wrong number of sends: %+v", state.Messages)
	}
	if _, err := service.RetryMessage(id, true); err == nil {
		t.Fatal("acknowledged message could be resent through retry")
	}
}

func TestChannelMessageRejectRetryAfterChannelChanges(t *testing.T) {
	var calls atomic.Int32
	service, update := connectedMessageService(t, &fakeMessageConnection{send: func(context.Context, string, string) error { calls.Add(1); return ErrMessagePermissionDenied }})
	pending := sendTestMessage(t, service, "not allowed")
	id := pending.Messages[0].ID
	waitForMessageResult(t, service, id, "failed")
	if _, err := service.RetryMessage(id, false); err != nil {
		t.Fatal(err)
	}
	waitForMessageResult(t, service, id, "failed")
	update(channelState("2"))
	if _, err := service.RetryMessage(id, true); err == nil || calls.Load() != 2 {
		t.Fatalf("old-channel retry was not rejected: %v", err)
	}
}

func TestChannelMessagesKeepPeerDuplicatesAndHistoryAcrossUpdates(t *testing.T) {
	service, update := connectedMessageService(t, &fakeMessageConnection{send: func(context.Context, string, string) error { return nil }})
	pending := sendTestMessage(t, service, "local")
	waitForMessageResult(t, service, pending.Messages[0].ID, "sent")
	remote := channelState("1")
	remote.Messages = []RemoteMessage{
		{ChannelID: "1", UserID: "self", Author: "Alice", Text: "local"},
		{ChannelID: "1", UserID: "peer", Author: "Peer", Text: "same"},
		{ChannelID: "1", UserID: "peer", Author: "Peer", Text: "same"},
	}
	update(remote)
	state, _ := service.GetWorkspace()
	if len(state.Messages) != 3 || state.Messages[1].ID == state.Messages[2].ID || state.Messages[1].Status != "received" || state.Messages[2].Text != "same" {
		t.Fatalf("self echo duplicated local row or peer messages collapsed: %+v", state.Messages)
	}
	state.Messages[1].Text = "mutated snapshot"
	update(channelState("1"))
	state, _ = service.GetWorkspace()
	if len(state.Messages) != 3 || state.Messages[1].Text != "same" || state.Messages[1].ChannelID != "1" {
		t.Fatalf("ordinary update erased or altered history: %+v", state.Messages)
	}
	update(channelState("2"))
	late := channelState("2")
	late.Messages = remote.Messages
	update(late)
	state, _ = service.GetWorkspace()
	if len(state.Messages) != 0 {
		t.Fatal("channel departure or late messages retained old history")
	}
	update(channelState("1"))
	state, _ = service.GetWorkspace()
	if len(state.Messages) != 0 {
		t.Fatal("returning restored old history")
	}
	update(remote)
	state, err := service.DisconnectServer()
	if err != nil || state.Session.Mode != "offline" || len(state.Messages) != 0 {
		t.Fatalf("disconnect retained history: %+v %v", state, err)
	}
}

func TestChannelMessageDisconnectCancelsPendingAndClearsHistory(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	connection := &fakeMessageConnection{send: func(ctx context.Context, _, _ string) error {
		close(started)
		<-ctx.Done()
		close(canceled)
		return ctx.Err()
	}}
	service, _ := connectedMessageService(t, connection)
	sendTestMessage(t, service, "pending")
	<-started
	state, err := service.DisconnectServer()
	if err != nil || state.Session.Mode != "offline" || state.Session.SendingMessageID != "" || len(state.Messages) != 0 {
		t.Fatalf("disconnect retained uncertain message: %+v %v", state, err)
	}
	select {
	case <-canceled:
	default:
		t.Fatal("disconnect returned before send cancellation completed")
	}
	if connection.closed.Load() != 1 {
		t.Fatal("connection was not closed exactly once")
	}
}

func TestChannelMessageTimeoutLeavesUnconfirmedHistory(t *testing.T) {
	service, _ := connectedMessageService(t, &fakeMessageConnection{send: func(ctx context.Context, _, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	}})
	service.messageTimeout = 10 * time.Millisecond
	pending := sendTestMessage(t, service, "timed out")
	state := waitForMessageResult(t, service, pending.Messages[0].ID, "unconfirmed")
	if len(state.Messages) != 1 || state.Messages[0].Error != ErrMessageDeliveryUnknown.Error() {
		t.Fatalf("timeout lost the uncertain result: %+v", state.Messages)
	}
}

func TestChannelMessageOldCompletionCannotChangeNewConnection(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{}, 1)
	defer close(release)
	service, oldUpdate := connectedMessageService(t, &fakeMessageConnection{send: func(ctx context.Context, _, _ string) error {
		close(started)
		<-ctx.Done()
		<-release
		return ErrMessagePermissionDenied
	}})
	pending := sendTestMessage(t, service, "old send")
	<-started
	oldUpdate(RemoteState{Closed: true})
	failed := waitForMode(t, service, "failed")
	if len(failed.Messages) != 1 || failed.Messages[0].Status != "unconfirmed" {
		t.Fatal("remote disconnect lost pending history")
	}
	service.mu.Lock()
	service.connector = connectorFunc(func(_ context.Context, _ ServerProfile, _ string, update func(RemoteState)) (RemoteConnection, error) {
		update(channelState("2"))
		return &fakeMessageConnection{send: func(context.Context, string, string) error { return nil }}, nil
	})
	service.mu.Unlock()
	if _, err := service.ConnectServer("home", ""); err != nil {
		t.Fatal(err)
	}
	current := waitForMode(t, service, "connected")
	if current.Session.ID == pending.Session.ID || len(current.Messages) != 0 {
		t.Fatal("new connection inherited old session identity or messages")
	}
	if _, err := service.SendChannelMessage(pending.Session.ID, "2", "old draft"); !errors.Is(err, ErrMessageChannelChanged) {
		t.Fatal("old session draft accepted")
	}
	newPending := sendTestMessage(t, service, "new send")
	current = waitForMessageResult(t, service, newPending.Messages[0].ID, "sent")
	stale := channelState("1")
	stale.Messages = []RemoteMessage{{ChannelID: "1", UserID: "old-peer", Text: "stale"}}
	oldUpdate(stale)
	// Let the old completion run, then verify its generation cannot affect the new row.
	// The deferred release also guarantees cleanup on an earlier assertion failure.
	release <- struct{}{}
	service.cleanupWG.Wait()
	current, _ = service.GetWorkspace()
	if len(current.Messages) != 1 || current.Messages[0].Text != "new send" || current.Messages[0].Status != "sent" || current.Session.ChannelID != "2" || current.Session.SendingMessageID != "" {
		t.Fatalf("old result changed new connection: %+v", current)
	}
}

func TestChannelMessagesCapPreservesActivePendingRow(t *testing.T) {
	started, ack := make(chan struct{}), make(chan struct{})
	service, update := connectedMessageService(t, &fakeMessageConnection{send: func(ctx context.Context, _, _ string) error {
		close(started)
		select {
		case <-ack:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}})
	pending := sendTestMessage(t, service, "pending oldest")
	<-started
	remote := channelState("1")
	for i := 0; i < 650; i++ {
		remote.Messages = append(remote.Messages, RemoteMessage{ChannelID: "1", UserID: "peer", Author: "Peer", Text: fmt.Sprintf("peer-%d", i)})
	}
	update(remote)
	state, _ := service.GetWorkspace()
	if len(state.Messages) != 500 || state.Messages[0].ID != pending.Messages[0].ID || state.Messages[0].Status != "sending" || state.Messages[499].Text != "peer-649" {
		t.Fatalf("history cap lost pending or newest row: count=%d", len(state.Messages))
	}
	close(ack)
	waitForMessageResult(t, service, pending.Messages[0].ID, "sent")
}
