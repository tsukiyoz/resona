package client

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestNotificationsKeepTransientEventsAndIgnoreSnapshots(t *testing.T) {
	service, update := connectedChannelService(t, &fakeConnection{})
	remote := channelState("1")
	remote.Users = append(remote.Users, User{ID: "peer", ChannelID: "1"})
	update(remote)
	state, _ := service.GetWorkspace()
	if len(state.Notifications) != 1 || state.Notifications[0].Kind != "connected" {
		t.Fatal("membership snapshot produced a notification or a nil queue")
	}
	remote.Events = []RemoteEvent{{Kind: "member_joined", UserID: "peer", ChannelID: "1"}}
	update(remote)
	remote.Users = remote.Users[:1]
	remote.Events = []RemoteEvent{{Kind: "member_left", UserID: "peer", ChannelID: "1"}}
	update(remote)
	state, _ = service.GetWorkspace()
	if len(state.Notifications) != 3 || state.Notifications[0].Kind != "connected" || state.Notifications[1].Kind != "member_joined" || state.Notifications[2].Kind != "member_left" {
		t.Fatalf("lost events between polls: %+v", state.Notifications)
	}
	for _, event := range state.Notifications {
		if event.ID == "" || event.ChannelID != "1" {
			t.Fatalf("invalid notification: %+v", event)
		}
		if _, err := time.Parse(time.RFC3339Nano, event.CreatedAt); err != nil {
			t.Fatal(err)
		}
	}
	if state.Notifications[0].ID == state.Notifications[1].ID || state.Notifications[1].ID == state.Notifications[2].ID {
		t.Fatal("events share an ID")
	}
	firstID := state.Notifications[0].ID
	state.Notifications[0].ID = "mutated"
	remote.Events = nil
	update(remote)
	state, _ = service.GetWorkspace()
	if len(state.Notifications) != 3 || state.Notifications[0].ID != firstID {
		t.Fatal("poll or snapshot replay modified queued notifications")
	}
}

func TestNotificationsFilterSelfOtherChannelsAndUnknownKinds(t *testing.T) {
	service, update := connectedChannelService(t, &fakeConnection{})
	remote := channelState("2")
	remote.Events = []RemoteEvent{
		{Kind: "member_joined", UserID: "self", ChannelID: "2"},
		{Kind: "member_left", UserID: "peer", ChannelID: "1"},
		{Kind: "member_joined", UserID: "", ChannelID: "2"},
		{Kind: "member_left", UserID: "peer", ChannelID: ""},
		{Kind: "unknown", UserID: "peer", ChannelID: "2"},
		{Kind: "member_joined", UserID: "peer", ChannelID: "2"},
	}
	update(remote)
	state, _ := service.GetWorkspace()
	if len(state.Notifications) != 2 || state.Notifications[0].Kind != "connected" || state.Notifications[1].ChannelID != "2" {
		t.Fatalf("incorrect event filtering: %+v", state.Notifications)
	}
	remote.SelfID = ""
	update(remote)
	state, _ = service.GetWorkspace()
	if len(state.Notifications) != 2 {
		t.Fatal("events played before own identity was known")
	}
}

func TestNotificationsBoundedAndSnapshotsDoNotAliasEviction(t *testing.T) {
	service, update := connectedChannelService(t, &fakeConnection{})
	remote := channelState("1")
	var first Workspace
	for i := range maxNotifications + 3 {
		remote.Events = []RemoteEvent{{Kind: "member_joined", UserID: fmt.Sprint(i), ChannelID: "1"}}
		update(remote)
		if i == 0 {
			first, _ = service.GetWorkspace()
		}
	}
	state, _ := service.GetWorkspace()
	if len(state.Notifications) != maxNotifications {
		t.Fatalf("queue length = %d", len(state.Notifications))
	}
	seen := map[string]bool{}
	for _, event := range state.Notifications {
		if seen[event.ID] || event.ID == first.Notifications[0].ID {
			t.Fatal("duplicate ID, failed eviction, or aliased snapshot")
		}
		seen[event.ID] = true
	}
}

func TestDisconnectedNotificationIsSingleAndSurvivesCleanup(t *testing.T) {
	for _, remoteClose := range []bool{false, true} {
		t.Run(fmt.Sprint(remoteClose), func(t *testing.T) {
			service, update := connectedChannelService(t, &fakeConnection{})
			if remoteClose {
				closed := channelState("1")
				closed.Closed = true
				closed.Events = []RemoteEvent{{Kind: "member_left", UserID: "peer", ChannelID: "1"}}
				update(closed)
				update(closed)
			}
			if _, err := service.DisconnectServer(); err != nil {
				t.Fatal(err)
			}
			state, err := service.DisconnectServer()
			if err != nil || state.Session.Mode != "offline" || len(state.Notifications) != 2 || state.Notifications[0].Kind != "connected" || state.Notifications[1].Kind != "disconnected" || state.Notifications[1].ChannelID != "1" {
				t.Fatalf("invalid disconnect notification state: %+v, %v", state, err)
			}
			id := state.Notifications[1].ID
			update(RemoteState{Closed: true})
			state, _ = service.GetWorkspace()
			if len(state.Notifications) != 2 || state.Notifications[1].ID != id {
				t.Fatal("stale callback added a disconnect event")
			}
		})
	}
}

func TestConnectingAndCancellationAreSilent(t *testing.T) {
	ready := make(chan struct{})
	service, _ := serviceWithProfile(t, connectorFunc(func(ctx context.Context, _ ServerProfile, _ string, update func(RemoteState)) (RemoteConnection, error) {
		remote := channelState("1")
		remote.Events = []RemoteEvent{{Kind: "member_joined", UserID: "peer", ChannelID: "1"}}
		update(remote)
		close(ready)
		<-ctx.Done()
		update(RemoteState{Closed: true})
		return nil, ctx.Err()
	}))
	if _, err := service.ConnectServer("home", ""); err != nil {
		t.Fatal(err)
	}
	<-ready
	state, _ := service.GetWorkspace()
	if len(state.Notifications) != 0 {
		t.Fatal("initial synchronization played notifications")
	}
	state, err := service.DisconnectServer()
	if err != nil || state.Notifications == nil || len(state.Notifications) != 0 {
		t.Fatalf("cancellation played notifications: %+v %v", state.Notifications, err)
	}
}

func TestNewSessionsClearNotificationHistoryAndShutdownIsSilent(t *testing.T) {
	service, oldUpdate := connectedChannelService(t, &fakeConnection{})
	if _, err := service.DisconnectServer(); err != nil {
		t.Fatal(err)
	}
	state, err := service.OpenPreview()
	if err != nil || state.Notifications == nil || len(state.Notifications) != 0 {
		t.Fatalf("preview retained notifications: %+v %v", state.Notifications, err)
	}
	if _, err := service.LeavePreview(); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConnectServer("home", ""); err != nil {
		t.Fatal(err)
	}
	waitForMode(t, service, "connected")
	old := channelState("1")
	old.Events = []RemoteEvent{{Kind: "member_joined", UserID: "peer", ChannelID: "1"}}
	oldUpdate(old)
	state, _ = service.GetWorkspace()
	if len(state.Notifications) != 1 || state.Notifications[0].Kind != "connected" {
		t.Fatal("previous session appended notifications")
	}
	id := state.Notifications[0].ID
	service.Shutdown()
	state, _ = service.GetWorkspace()
	if len(state.Notifications) != 1 || state.Notifications[0].ID != id {
		t.Fatal("shutdown played a disconnect notification")
	}
}

func TestReconnectStartsWithEmptyNotifications(t *testing.T) {
	service, _ := connectedChannelService(t, &fakeConnection{})
	previous, _ := service.GetWorkspace()
	previousID := previous.Notifications[0].ID
	if _, err := service.DisconnectServer(); err != nil {
		t.Fatal(err)
	}
	state, err := service.ConnectServer("home", "")
	if err != nil || state.Notifications == nil || len(state.Notifications) != 0 {
		t.Fatalf("reconnect retained prior notifications: %+v %v", state.Notifications, err)
	}
	state = waitForMode(t, service, "connected")
	if len(state.Notifications) != 1 || state.Notifications[0].Kind != "connected" || state.Notifications[0].ID == previousID {
		t.Fatal("reconnect replayed prior notifications")
	}
}

func TestRemoteCloseDuringInitialSyncIsSilent(t *testing.T) {
	service, _ := serviceWithProfile(t, connectorFunc(func(_ context.Context, _ ServerProfile, _ string, update func(RemoteState)) (RemoteConnection, error) {
		update(RemoteState{Closed: true})
		return &fakeConnection{}, nil
	}))
	t.Cleanup(service.Shutdown)
	if _, err := service.ConnectServer("home", ""); err != nil {
		t.Fatal(err)
	}
	state := waitForMode(t, service, "failed")
	if state.Notifications == nil || len(state.Notifications) != 0 {
		t.Fatal("connection setup failure played a disconnect notification")
	}
}

func TestConnectedNotificationWaitsForSuccessfulSetupAndOccursOnce(t *testing.T) {
	updates := make(chan func(RemoteState), 1)
	finish := make(chan struct{})
	service, _ := serviceWithProfile(t, connectorFunc(func(_ context.Context, _ ServerProfile, _ string, update func(RemoteState)) (RemoteConnection, error) {
		update(channelState("1"))
		updates <- update
		<-finish
		return &fakeConnection{}, nil
	}))
	t.Cleanup(service.Shutdown)
	if _, err := service.ConnectServer("home", ""); err != nil {
		t.Fatal(err)
	}
	update := <-updates
	initial, _ := service.GetWorkspace()
	close(finish)
	if initial.Session.Mode != "connecting" || len(initial.Notifications) != 0 {
		t.Fatalf("initial snapshot announced success before Connect returned: %+v", initial)
	}
	state := waitForMode(t, service, "connected")
	if len(state.Notifications) != 1 || state.Notifications[0].Kind != "connected" || state.Notifications[0].ChannelID != "1" {
		t.Fatalf("missing connected notification: %+v", state.Notifications)
	}
	id := state.Notifications[0].ID
	update(channelState("1"))
	update(channelState("2"))
	state, _ = service.GetWorkspace()
	if len(state.Notifications) != 1 || state.Notifications[0].ID != id {
		t.Fatal("state refresh or own channel move replayed connection success")
	}
}

func TestFailedSetupNeverAnnouncesConnectionSuccess(t *testing.T) {
	for _, setupError := range []error{nil, errors.New("setup failed")} {
		t.Run(fmt.Sprint(setupError), func(t *testing.T) {
			service, _ := serviceWithProfile(t, connectorFunc(func(_ context.Context, _ ServerProfile, _ string, update func(RemoteState)) (RemoteConnection, error) {
				update(channelState("1"))
				return nil, setupError
			}))
			t.Cleanup(service.Shutdown)
			if _, err := service.ConnectServer("home", ""); err != nil {
				t.Fatal(err)
			}
			state := waitForMode(t, service, "failed")
			if len(state.Notifications) != 0 {
				t.Fatalf("unsuccessful setup announced success: %+v", state.Notifications)
			}
		})
	}
}

func TestCanceledSetupLateSuccessDoesNotNotify(t *testing.T) {
	started := make(chan struct{})
	connection := &fakeConnection{}
	service, _ := serviceWithProfile(t, connectorFunc(func(ctx context.Context, _ ServerProfile, _ string, update func(RemoteState)) (RemoteConnection, error) {
		update(channelState("1"))
		close(started)
		<-ctx.Done()
		return connection, nil
	}))
	t.Cleanup(service.Shutdown)
	if _, err := service.ConnectServer("home", ""); err != nil {
		t.Fatal(err)
	}
	<-started
	state, err := service.DisconnectServer()
	if err != nil || state.Session.Mode != "offline" || len(state.Notifications) != 0 || connection.closed.Load() != 1 {
		t.Fatalf("canceled setup accepted late success: %+v, closed=%d, error=%v", state, connection.closed.Load(), err)
	}
}
