package desktopipc

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tsukiyoz/resona/internal/client"
)

type shutdownConnection struct {
	closeStarted chan struct{}
	release      chan struct{}
	closed       atomic.Bool
}

func (c *shutdownConnection) MoveChannel(context.Context, string) error {
	return errors.New("not supported")
}

func (c *shutdownConnection) Close() error {
	if c.closeStarted != nil {
		close(c.closeStarted)
	}
	if c.release != nil {
		<-c.release
	}
	c.closed.Store(true)
	return nil
}

type shutdownConnector struct{ connection *shutdownConnection }

func (c shutdownConnector) Connect(context.Context, client.ServerProfile, string, func(client.RemoteState)) (client.RemoteConnection, error) {
	return c.connection, nil
}

func shutdownService(t *testing.T, connection *shutdownConnection) *client.Service {
	t.Helper()
	s, err := client.NewWithConnector(&memoryProfiles{profiles: []client.ServerProfile{{Protocol: "resona-noise", ServerPublicKey: "abababababababababababababababababababababababababababababababab", ID: "test", Name: "Test", Address: "example.invalid", Nickname: "Tester"}}}, shutdownConnector{connection})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConnectServer("test", ""); err != nil {
		t.Fatal(err)
	}
	changes, unsubscribe := s.SubscribeChanges()
	defer unsubscribe()
	deadline := time.After(time.Second)
	for {
		w, _ := s.GetWorkspace()
		if w.Session.Mode == "connected" {
			return s
		}
		select {
		case <-changes:
		case <-deadline:
			t.Fatal("fake connect did not finish")
		}
	}
}

func TestPrepareShutdownWaitsForTeardownAndSound(t *testing.T) {
	connection := &shutdownConnection{closeStarted: make(chan struct{}), release: make(chan struct{})}
	service := shutdownService(t, connection)
	started, finish := make(chan struct{}), make(chan struct{})
	done := make(chan shutdownStatus, 1)
	go func() {
		done <- prepareShutdown(context.Background(), service, true, 65, func(_ context.Context, kind string, volume int) error {
			if !connection.closed.Load() {
				t.Error("sound started before transport closed")
			}
			if kind != "disconnected" || volume != 65 {
				t.Error("incorrect shutdown sound")
			}
			close(started)
			<-finish
			return nil
		})
	}()
	<-connection.closeStarted
	select {
	case <-started:
		t.Fatal("sound played during teardown")
	case <-done:
		t.Fatal("shutdown acknowledged before teardown")
	default:
	}
	close(connection.release)
	<-started
	select {
	case <-done:
		t.Fatal("acknowledged before sound completed")
	default:
	}
	close(finish)
	select {
	case status := <-done:
		if !status.Disconnected || !status.SoundCompleted || status.Error != "" {
			t.Fatalf("incorrect completion: %+v", status)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not complete")
	}
}

func TestPrepareShutdownSilentStatesAndPreferences(t *testing.T) {
	for _, scenario := range []string{"offline", "preview", "disabled", "zero volume"} {
		t.Run(scenario, func(t *testing.T) {
			var service *client.Service
			enabled, volume := true, 65
			switch scenario {
			case "disabled", "zero volume":
				service = shutdownService(t, &shutdownConnection{})
				if scenario == "disabled" {
					enabled = false
				} else {
					volume = 0
				}
			default:
				service, _ = client.New(&memoryProfiles{})
				if scenario == "preview" {
					_, _ = service.OpenPreview()
				}
			}
			status := prepareShutdown(context.Background(), service, enabled, volume, func(context.Context, string, int) error { t.Error("silent shutdown played audio"); return nil })
			if !status.Disconnected || status.SoundCompleted || status.Error != "" {
				t.Fatalf("silent result: %+v", status)
			}
		})
	}
}

func TestPrepareShutdownSoundFailureDoesNotBlockExit(t *testing.T) {
	service := shutdownService(t, &shutdownConnection{})
	status := prepareShutdown(context.Background(), service, true, 65, func(context.Context, string, int) error { return errors.New("device failed") })
	if !status.Disconnected || status.SoundCompleted || status.Error == "" {
		t.Fatalf("failure result: %+v", status)
	}
}

func TestPrepareShutdownContextBoundsSoundWait(t *testing.T) {
	service := shutdownService(t, &shutdownConnection{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	playerDone := make(chan struct{})
	status := prepareShutdown(ctx, service, true, 65, func(ctx context.Context, _ string, _ int) error {
		defer close(playerDone)
		cancel()
		<-ctx.Done()
		return ctx.Err()
	})
	<-playerDone
	if !status.Disconnected || status.SoundCompleted || status.Error == "" {
		t.Fatalf("cancelled sound result: %+v", status)
	}
}

func TestPrepareShutdownContextBoundsTransportWait(t *testing.T) {
	connection := &shutdownConnection{closeStarted: make(chan struct{}), release: make(chan struct{})}
	service := shutdownService(t, connection)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan shutdownStatus, 1)
	go func() {
		done <- prepareShutdown(ctx, service, true, 65, func(context.Context, string, int) error { t.Error("sound played before teardown"); return nil })
	}()
	<-connection.closeStarted
	cancel()
	select {
	case status := <-done:
		if status.Disconnected || status.SoundCompleted || status.Error == "" {
			t.Fatalf("pending teardown result: %+v", status)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled teardown wait blocked")
	}
	close(connection.release)
	service.Shutdown()
}
