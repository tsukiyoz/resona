package desktopipc

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/tsukiyoz/resona/internal/client"
)

type blockingDetails struct {
	shutdownConnection
	started  chan struct{}
	canceled chan struct{}
}

func (c *blockingDetails) ReadChannelDetails(ctx context.Context, id string) (client.ChannelDetails, error) {
	if id == "first" {
		close(c.started)
		<-ctx.Done()
		close(c.canceled)
		return client.ChannelDetails{}, ctx.Err()
	}
	return client.ChannelDetails{ID: id}, nil
}

type detailsConnector struct{ connection *blockingDetails }

func (c detailsConnector) Connect(context.Context, client.ServerProfile, string, func(client.RemoteState)) (client.RemoteConnection, error) {
	return c.connection, nil
}

func TestNewSelectionAndNavigationCancelObsoleteDetails(t *testing.T) {
	for _, next := range []string{"GetChannelDetails", "SelectChannel", "CancelDetails"} {
		t.Run(next, func(t *testing.T) {
			connection := &blockingDetails{started: make(chan struct{}), canceled: make(chan struct{})}
			service, err := client.NewWithConnector(&memoryProfiles{profiles: []client.ServerProfile{{Protocol: "resona-noise", ServerPublicKey: "abababababababababababababababababababababababababababababababab", ID: "test", Name: "Test", Address: "example.invalid", Nickname: "Tester"}}}, detailsConnector{connection})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Shutdown()
			if _, err := service.ConnectServer("test", ""); err != nil {
				t.Fatal(err)
			}
			changes, unsubscribe := service.SubscribeChanges()
			defer unsubscribe()
			var workspace client.Workspace
			for {
				workspace, _ = service.GetWorkspace()
				if workspace.Session.Mode == "connected" {
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
			_ = parent.SetDeadline(time.Now().Add(3 * time.Second))
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- Run(ctx, service, child, child) }()
			defer func() { cancel(); <-done }()
			responses := make(chan envelope, 32)
			go func() {
				decoder := json.NewDecoder(parent)
				for {
					var response envelope
					if decoder.Decode(&response) != nil {
						return
					}
					select {
					case responses <- response:
					case <-ctx.Done():
						return
					}
				}
			}()
			encoder := json.NewEncoder(parent)
			if err := encoder.Encode(map[string]any{"id": 1, "method": "GetChannelDetails", "params": map[string]string{"sessionID": workspace.Session.ID, "channelID": "first"}}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-connection.started:
			case <-time.After(time.Second):
				t.Fatal("read did not start")
			}
			params := map[string]string{"sessionID": workspace.Session.ID, "channelID": "second"}
			if next == "SelectChannel" {
				params = map[string]string{"id": "second"}
			}
			if err := encoder.Encode(map[string]any{"id": 2, "method": next, "params": params}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-connection.canceled:
			case <-time.After(time.Second):
				t.Fatal("obsolete read retained command gate")
			}
			for {
				select {
				case response := <-responses:
					if response.ID == 1 {
						if response.Error != "详情读取已取消" {
							t.Fatalf("raw cancellation surfaced: %s", response.Error)
						}
						return
					}
				case <-time.After(time.Second):
					t.Fatal("missing canceled response")
				}
			}
		})
	}
}
