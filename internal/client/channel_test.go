package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func channelState(id string) RemoteState {
	return RemoteState{
		ServerName: "Test 服务器", ChannelID: id, SelfID: "self",
		Channels: []Channel{{ID: "1", Name: "Lobby"}, {ID: "2", Name: "Test"}, {ID: "3", Name: "Locked", PasswordRequired: true}},
		Users:    []User{{ID: "self", ChannelID: id, Nickname: "Alice", Self: true}},
	}
}

func connectedChannelService(t *testing.T, connection *fakeConnection) (*Service, func(RemoteState)) {
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

func waitForMove(t *testing.T, service *Service) Workspace {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state, _ := service.GetWorkspace()
		if state.Session.SwitchingChannelID == "" {
			return state
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("channel move did not complete")
	return Workspace{}
}

func TestRemoteChannelUsesOnlyObservedLocation(t *testing.T) {
	for _, updateFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("update-first=%t", updateFirst), func(t *testing.T) {
			started, finish := make(chan string, 1), make(chan struct{})
			connection := &fakeConnection{move: func(ctx context.Context, id string) error {
				started <- id
				select {
				case <-finish:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}}
			service, update := connectedChannelService(t, connection)
			state, err := service.SelectChannel("2")
			if err != nil || state.Session.ChannelID != "1" || state.Session.SwitchingChannelID != "2" {
				t.Fatalf("move changed location before a server event: %+v %v", state.Session, err)
			}
			if id := <-started; id != "2" {
				t.Fatalf("unexpected target %q", id)
			}
			if updateFirst {
				update(channelState("2"))
				state, _ = service.GetWorkspace()
				if state.Session.ChannelID != "2" || state.Session.SwitchingChannelID != "2" {
					t.Fatalf("server state was not applied during move: %+v", state.Session)
				}
			}
			close(finish)
			state = waitForMove(t, service)
			if !updateFirst {
				if state.Session.ChannelID != "1" {
					t.Fatalf("completion optimistically changed location: %+v", state.Session)
				}
				update(channelState("2"))
			}
			state, _ = service.GetWorkspace()
			if state.Session.ChannelID != "2" || state.Session.Error != "" || state.Users[0].ChannelID != "2" {
				t.Fatalf("unexpected completed move: %+v", state)
			}
		})
	}
}

func TestRemoteChannelFailurePersistsUntilNextOperation(t *testing.T) {
	for _, failure := range []error{errors.New("private protocol payload"), fmt.Errorf("wrapped: %w", ErrChannelPermissionDenied), ErrChannelPasswordRequired, context.DeadlineExceeded} {
		t.Run(channelMoveError(failure), func(t *testing.T) {
			connection := &fakeConnection{move: func(context.Context, string) error { return failure }}
			service, update := connectedChannelService(t, connection)
			if _, err := service.SelectChannel("2"); err != nil {
				t.Fatal(err)
			}
			state := waitForMove(t, service)
			if state.Session.ChannelID != "1" || state.Session.Error != channelMoveError(failure) || strings.Contains(state.Session.Error, "private") {
				t.Fatalf("unexpected failure state: %+v", state.Session)
			}
			update(channelState("1"))
			state, _ = service.GetWorkspace()
			if state.Session.Error != channelMoveError(failure) {
				t.Fatalf("ordinary update erased operation error: %+v", state.Session)
			}
			state, err := service.SelectChannel("2")
			if err != nil || state.Session.Error != "" {
				t.Fatalf("retry did not clear old operation error: %+v %v", state.Session, err)
			}
			waitForMove(t, service)
			update(RemoteState{Closed: true})
			state = waitForMode(t, service, "failed")
			if state.Session.Error != "服务器连接已关闭" {
				t.Fatalf("old move error masked disconnection: %+v", state.Session)
			}
		})
	}
}

func TestRemoteChannelValidationAndSingleFlight(t *testing.T) {
	started := make(chan struct{}, 1)
	connection := &fakeConnection{move: func(ctx context.Context, _ string) error {
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}}
	service, _ := connectedChannelService(t, connection)
	for _, id := range []string{"missing", "3"} {
		if _, err := service.SelectChannel(id); err == nil {
			t.Fatalf("invalid channel %q accepted", id)
		}
	}
	if state, err := service.SelectChannel("1"); err != nil || state.Session.SwitchingChannelID != "" {
		t.Fatalf("current channel should be a no-op: %+v %v", state.Session, err)
	}
	if _, err := service.SelectChannel("2"); err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := service.SelectChannel("2"); err == nil {
		t.Fatal("duplicate move accepted")
	}
	if _, err := service.SelectChannel("1"); err == nil {
		t.Fatal("second move accepted during pending operation")
	}
	state, err := service.DisconnectServer()
	if err != nil || state.Session.Mode != "offline" || state.Session.SwitchingChannelID != "" || state.Session.Error != "" || connection.closed.Load() != 1 {
		t.Fatalf("disconnect did not cancel move cleanly: %+v %v", state.Session, err)
	}
}

func TestRemoteChannelTimeout(t *testing.T) {
	connection := &fakeConnection{move: func(ctx context.Context, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	service, _ := connectedChannelService(t, connection)
	service.moveTimeout = 10 * time.Millisecond
	if _, err := service.SelectChannel("2"); err != nil {
		t.Fatal(err)
	}
	state := waitForMove(t, service)
	if state.Session.ChannelID != "1" || !strings.Contains(state.Session.Error, "超时") {
		t.Fatalf("unexpected timed out move: %+v", state.Session)
	}
}

func TestRemoteChannelOldCompletionCannotChangeNewSession(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	connection := &fakeConnection{move: func(ctx context.Context, _ string) error {
		close(started)
		<-ctx.Done()
		<-release
		return ErrChannelPermissionDenied
	}}
	service, oldUpdate := connectedChannelService(t, connection)
	if _, err := service.SelectChannel("2"); err != nil {
		t.Fatal(err)
	}
	<-started
	oldUpdate(RemoteState{Closed: true})
	service.mu.Lock()
	service.connector = connectorFunc(func(_ context.Context, _ ServerProfile, _ string, update func(RemoteState)) (RemoteConnection, error) {
		update(channelState("2"))
		return &fakeConnection{}, nil
	})
	service.mu.Unlock()
	if _, err := service.ConnectServer("home", ""); err != nil {
		close(release)
		t.Fatal(err)
	}
	waitForMode(t, service, "connected")
	close(release)
	service.cleanupWG.Wait()
	state, _ := service.GetWorkspace()
	if state.Session.Mode != "connected" || state.Session.ChannelID != "2" || state.Session.Error != "" || state.Session.SwitchingChannelID != "" {
		t.Fatalf("stale completion changed new session: %+v", state.Session)
	}
}

func TestRemoteChannelShutdownCancelsMove(t *testing.T) {
	started := make(chan struct{})
	connection := &fakeConnection{move: func(ctx context.Context, _ string) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}
	service, _ := connectedChannelService(t, connection)
	if _, err := service.SelectChannel("2"); err != nil {
		t.Fatal(err)
	}
	<-started
	service.Shutdown()
	state, _ := service.GetWorkspace()
	if state.Session.Mode != "offline" || state.Session.SwitchingChannelID != "" || connection.closed.Load() != 1 {
		t.Fatalf("shutdown did not cancel move: %+v", state.Session)
	}
}

func TestPreviewChannelSelectionRemainsImmediate(t *testing.T) {
	service, _ := serviceWithProfile(t, nil)
	if _, err := service.OpenPreview(); err != nil {
		t.Fatal(err)
	}
	state, err := service.SelectChannel("music")
	if err != nil || state.Session.ChannelID != "music" || state.Session.SwitchingChannelID != "" || state.Users[0].ChannelID != "music" {
		t.Fatalf("preview channel selection changed: %+v %v", state.Session, err)
	}
}
