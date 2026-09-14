package client

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type connectorFunc func(context.Context, ServerProfile, string, func(RemoteState)) (RemoteConnection, error)

func (f connectorFunc) Connect(ctx context.Context, profile ServerProfile, password string, update func(RemoteState)) (RemoteConnection, error) {
	return f(ctx, profile, password, update)
}

type fakeConnection struct {
	closed atomic.Int32
	move   func(context.Context, string) error
}

func (c *fakeConnection) MoveChannel(ctx context.Context, id string) error {
	if c.move != nil {
		return c.move(ctx, id)
	}
	return errors.New("move not configured")
}

func (c *fakeConnection) Close() error {
	c.closed.Add(1)
	return nil
}

func serviceWithProfile(t *testing.T, connector RemoteConnector) (*Service, ServerProfile) {
	t.Helper()
	profile := ServerProfile{Protocol: "resona-noise", ServerPublicKey: "abababababababababababababababababababababababababababababababab", ID: "home", Name: "Home", Address: "localhost:9988", Nickname: "Alice"}
	service, err := NewWithConnector(&memoryStore{profiles: []ServerProfile{profile}}, connector)
	if err != nil {
		t.Fatal(err)
	}
	return service, profile
}

func waitForMode(t *testing.T, service *Service, mode string) Workspace {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state, err := service.GetWorkspace()
		if err != nil {
			t.Fatal(err)
		}
		if state.Session.Mode == mode {
			return state
		}
		time.Sleep(time.Millisecond)
	}
	state, _ := service.GetWorkspace()
	t.Fatalf("session did not reach %q: %+v", mode, state.Session)
	return Workspace{}
}

func TestRemoteConnectionPublishesSnapshot(t *testing.T) {
	connection := &fakeConnection{}
	connector := connectorFunc(func(_ context.Context, profile ServerProfile, password string, update func(RemoteState)) (RemoteConnection, error) {
		if profile.ID != "home" || password != "runtime-only" {
			t.Fatalf("unexpected connect arguments: %+v %q", profile, password)
		}
		update(RemoteState{
			ServerName: "Test Resona", ChannelID: "10", SelfID: "42", IdentityUID: "uid-1",
			Channels: []Channel{{ID: "10", Name: "Lobby", ParentID: "0", Order: "1", Members: 1}},
			Users:    []User{{ID: "42", Nickname: "Alice", ChannelID: "10", Self: true}},
		})
		return connection, nil
	})
	service, _ := serviceWithProfile(t, connector)

	initial, err := service.ConnectServer("home", "runtime-only")
	if err != nil || initial.Session.Mode != "connecting" {
		t.Fatalf("connect did not start asynchronously: %+v %v", initial.Session, err)
	}
	state := waitForMode(t, service, "connected")
	if state.Session.ServerID != "home" || state.Session.ServerName != "Test Resona" || state.Session.ChannelID != "10" || state.Session.SelfID != "42" || state.Session.IdentityUID != "uid-1" {
		t.Fatalf("unexpected connected session: %+v", state.Session)
	}
	if len(state.Channels) != 1 || state.Channels[0].ParentID != "0" || state.Channels[0].Order != "1" || len(state.Users) != 1 || !state.Users[0].Self {
		t.Fatalf("unexpected remote snapshot: %+v %+v", state.Channels, state.Users)
	}
	state.Channels[0].Name = "mutated"
	state.Users[0].Nickname = "mutated"
	state, _ = service.GetWorkspace()
	if state.Channels[0].Name != "Lobby" || state.Users[0].Nickname != "Alice" {
		t.Fatal("remote snapshot aliases internal state")
	}
	for _, profile := range state.Servers {
		if strings.Contains(profile.Name+profile.Address+profile.Nickname, "runtime-only") {
			t.Fatal("password leaked into persisted workspace")
		}
	}
}

func TestSessionProtocolIsFixedAtConnectAdmission(t *testing.T) {
	connector := connectorFunc(func(_ context.Context, _ ServerProfile, _ string, update func(RemoteState)) (RemoteConnection, error) {
		update(RemoteState{ServerName: "Native", ChannelID: "1", SelfID: "1", Channels: []Channel{{ID: "1", Name: "Lobby"}}, Users: []User{{ID: "1", Self: true, ChannelID: "1"}}})
		return &fakeConnection{}, nil
	})
	s, _ := serviceWithProfile(t, connector)
	defer s.Shutdown()
	s.mu.Lock()
	s.state.Servers[0].Protocol = "resona-noise"
	s.mu.Unlock()
	initial, err := s.ConnectServer("home", "")
	if err != nil || initial.Session.Protocol != "resona-noise" {
		t.Fatal("connecting protocol missing")
	}
	waitForMode(t, s, "connected")
	s.mu.Lock()
	s.state.Servers[0].Protocol = "unsupported"
	s.mu.Unlock()
	state, _ := s.GetWorkspace()
	if state.Session.Protocol != "resona-noise" {
		t.Fatal("bookmark edit changed live protocol")
	}
}

func TestRemoteConnectTimeoutIsSanitized(t *testing.T) {
	secret := "sensitive-adapter-detail"
	connector := connectorFunc(func(ctx context.Context, _ ServerProfile, _ string, _ func(RemoteState)) (RemoteConnection, error) {
		<-ctx.Done()
		return nil, errors.New(secret)
	})
	service, _ := serviceWithProfile(t, connector)
	service.connectTimeout = 10 * time.Millisecond
	if _, err := service.ConnectServer("home", "password"); err != nil {
		t.Fatal(err)
	}
	state := waitForMode(t, service, "failed")
	if !strings.Contains(state.Session.Error, "超时") || strings.Contains(state.Session.Error, secret) {
		t.Fatalf("unsafe timeout error: %q", state.Session.Error)
	}
	if state.Channels == nil || state.Users == nil || state.Messages == nil {
		t.Fatal("failed workspace contains nil collections")
	}
}

func TestDisconnectCancelsPendingConnectAndIgnoresOldUpdates(t *testing.T) {
	var mu sync.Mutex
	var updates []func(RemoteState)
	connectStarted := make(chan struct{}, 2)
	connector := connectorFunc(func(ctx context.Context, _ ServerProfile, _ string, update func(RemoteState)) (RemoteConnection, error) {
		mu.Lock()
		updates = append(updates, update)
		call := len(updates)
		mu.Unlock()
		connectStarted <- struct{}{}
		if call == 1 {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		update(RemoteState{ServerName: "Current", ChannelID: "2", Channels: []Channel{}, Users: []User{}})
		return &fakeConnection{}, nil
	})
	service, _ := serviceWithProfile(t, connector)
	if _, err := service.ConnectServer("home", "first"); err != nil {
		t.Fatal(err)
	}
	<-connectStarted
	state, err := service.DisconnectServer()
	if err != nil || state.Session.Mode != "offline" {
		t.Fatalf("pending disconnect failed: %+v %v", state.Session, err)
	}
	if _, err := service.ConnectServer("home", "second"); err != nil {
		t.Fatal(err)
	}
	<-connectStarted
	state = waitForMode(t, service, "connected")
	mu.Lock()
	oldUpdate := updates[0]
	mu.Unlock()
	oldUpdate(RemoteState{ServerName: "Stale", ChannelID: "999", Closed: true, Error: "旧错误"})
	state, _ = service.GetWorkspace()
	if state.Session.Mode != "connected" || state.Session.ServerName != "Current" || state.Session.ChannelID != "2" {
		t.Fatalf("stale callback changed the active session: %+v", state.Session)
	}
}

func TestConnectedSessionRejectsDuplicateAndUnsupportedOperations(t *testing.T) {
	connection := &fakeConnection{}
	connector := connectorFunc(func(_ context.Context, _ ServerProfile, _ string, update func(RemoteState)) (RemoteConnection, error) {
		update(RemoteState{ServerName: "Live", Channels: []Channel{}, Users: []User{}})
		return connection, nil
	})
	service, profile := serviceWithProfile(t, connector)
	if _, err := service.ConnectServer(profile.ID, "password"); err != nil {
		t.Fatal(err)
	}
	waitForMode(t, service, "connected")
	for name, operation := range map[string]func() error{
		"duplicate connect": func() error { _, err := service.ConnectServer(profile.ID, "password"); return err },
		"unknown channel":   func() error { _, err := service.SelectChannel("1"); return err },
		"send message":      func() error { _, err := service.SendMessage("hello"); return err },
		"open preview":      func() error { _, err := service.OpenPreview(); return err },
		"edit profile":      func() error { profile.Name = "Changed"; _, err := service.SaveServer(profile); return err },
		"delete profile":    func() error { _, err := service.DeleteServer(profile.ID); return err },
	} {
		if err := operation(); err == nil {
			t.Errorf("%s unexpectedly succeeded", name)
		}
	}
	state, err := service.DisconnectServer()
	if err != nil || state.Session.Mode != "offline" || connection.closed.Load() != 1 {
		t.Fatalf("disconnect did not release connection: %+v closed=%d err=%v", state.Session, connection.closed.Load(), err)
	}
}

func TestRemoteClosedUpdateReleasesConnection(t *testing.T) {
	connection := &fakeConnection{}
	updates := make(chan func(RemoteState), 1)
	connector := connectorFunc(func(_ context.Context, _ ServerProfile, _ string, update func(RemoteState)) (RemoteConnection, error) {
		updates <- update
		return connection, nil
	})
	service, profile := serviceWithProfile(t, connector)
	if _, err := service.ConnectServer(profile.ID, "password"); err != nil {
		t.Fatal(err)
	}
	waitForMode(t, service, "connected")
	update := <-updates
	update(RemoteState{Closed: true, Error: "服务器已断开连接"})
	state := waitForMode(t, service, "failed")
	if state.Session.Error != "服务器已断开连接" {
		t.Fatalf("unexpected closed state: %+v", state.Session)
	}
	state, err := service.DisconnectServer()
	if err != nil || state.Session.Mode != "offline" || connection.closed.Load() != 1 {
		t.Fatalf("closed connection cleanup failed: %+v closed=%d err=%v", state.Session, connection.closed.Load(), err)
	}
}

func TestShutdownReleasesConnection(t *testing.T) {
	connection := &fakeConnection{}
	connector := connectorFunc(func(_ context.Context, _ ServerProfile, _ string, _ func(RemoteState)) (RemoteConnection, error) {
		return connection, nil
	})
	service, profile := serviceWithProfile(t, connector)
	if _, err := service.ConnectServer(profile.ID, "password"); err != nil {
		t.Fatal(err)
	}
	waitForMode(t, service, "connected")
	service.Shutdown()
	state, _ := service.GetWorkspace()
	if connection.closed.Load() != 1 || state.Session.Mode != "offline" {
		t.Fatalf("shutdown did not release connection: closed=%d state=%+v", connection.closed.Load(), state.Session)
	}
	if _, err := service.ConnectServer(profile.ID, "password"); err == nil {
		t.Fatal("connect succeeded after shutdown")
	}
}
