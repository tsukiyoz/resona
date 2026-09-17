package desktopipc

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/tsukiyoz/resona/internal/client"
)

type blockingChannel struct {
	shutdownConnection
	started chan struct{}
}

func (c *blockingChannel) CreateChannel(ctx context.Context, _, _ string) error {
	close(c.started)
	<-ctx.Done()
	return ctx.Err()
}

func (c *blockingChannel) UpdateChannel(ctx context.Context, _, _, _ string) error {
	return c.CreateChannel(ctx, "", "")
}

func (c *blockingChannel) DeleteChannel(ctx context.Context, _ string) error {
	return c.CreateChannel(ctx, "", "")
}

type channelConnector struct{ connection *blockingChannel }

func (c channelConnector) Connect(_ context.Context, _ client.ServerProfile, _ string, update func(client.RemoteState)) (client.RemoteConnection, error) {
	update(client.RemoteState{CanManageChannels: true, ServerRole: "owner", ChannelID: "1", SelfID: "self", Channels: []client.Channel{{ID: "1", Name: "Lobby"}}})
	return c.connection, nil
}

func TestPendingChannelCommandDoesNotBlockIPCOrShutdown(t *testing.T) {
	c := &blockingChannel{started: make(chan struct{})}
	s, err := client.NewWithConnector(&memoryProfiles{profiles: []client.ServerProfile{{Protocol: "resona-noise", ServerPublicKey: "abababababababababababababababababababababababababababababababab", ID: "test", Name: "Test", Address: "example.invalid", Nickname: "Tester"}}}, channelConnector{c})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown()
	changes, unsub := s.SubscribeChanges()
	defer unsub()
	if _, err = s.ConnectServer("test", ""); err != nil {
		t.Fatal(err)
	}
	var workspace client.Workspace
	for {
		workspace, _ = s.GetWorkspace()
		if workspace.Session.Mode == "connected" && workspace.Session.CanManageChannels {
			break
		}
		select {
		case <-changes:
		case <-time.After(time.Second):
			t.Fatal("connect timeout")
		}
	}
	parent, child := net.Pipe()
	defer parent.Close()
	_ = parent.SetDeadline(time.Now().Add(4 * time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, s, child, child) }()
	responses := make(chan envelope, 32)
	go func() {
		d := json.NewDecoder(parent)
		for {
			var r envelope
			if d.Decode(&r) != nil {
				return
			}
			select {
			case responses <- r:
			case <-ctx.Done():
				return
			}
		}
	}()
	e := json.NewEncoder(parent)
	if err = e.Encode(map[string]any{"id": 1, "method": "CreateChannel", "params": map[string]string{"sessionID": workspace.Session.ID, "name": "New"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.started:
	case <-time.After(time.Second):
		t.Fatal("mutation not dispatched")
	}
	if err = e.Encode(map[string]any{"id": 2, "method": "GetWorkspace"}); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case r := <-responses:
			if r.ID == 2 {
				goto shutdown
			}
		case <-time.After(time.Second):
			t.Fatal("management blocked snapshot response")
		}
	}
shutdown:
	if err = e.Encode(map[string]any{"id": 3, "method": "Shutdown"}); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("management blocked shutdown")
	}
}
