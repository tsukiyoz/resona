package client

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type memoryPasswords struct {
	mu                        sync.Mutex
	values                    map[string]string
	reads                     int
	setErr, getErr, deleteErr error
}

func (p *memoryPasswords) Has(key string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reads++
	_, found := p.values[key]
	return found, nil
}

func (p *memoryPasswords) Get(key string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reads++
	if p.getErr != nil {
		return "", p.getErr
	}
	value, found := p.values[key]
	if !found {
		return "", errors.New("missing")
	}
	return value, nil
}

func (p *memoryPasswords) Set(key, value string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.setErr != nil {
		return p.setErr
	}
	p.values[key] = value
	return nil
}

func (p *memoryPasswords) Delete(key string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.deleteErr != nil {
		return p.deleteErr
	}
	delete(p.values, key)
	return nil
}

func passwordService(t *testing.T, connector RemoteConnector) (*Service, *memoryPasswords, *memoryStore) {
	t.Helper()
	profiles := &memoryStore{profiles: []ServerProfile{
		{Protocol: "resona-noise", ServerPublicKey: "abababababababababababababababababababababababababababababababab", ID: "one", Name: "One", Address: "one.invalid", Nickname: "Tester"},
		{Protocol: "resona-noise", ServerPublicKey: "abababababababababababababababababababababababababababababababab", ID: "two", Name: "Two", Address: "two.invalid", Nickname: "Tester"},
	}}
	passwords := &memoryPasswords{values: map[string]string{}}
	s, err := NewWithPasswordStore(profiles, connector, passwords)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)
	return s, passwords, profiles
}

func waitConnectFinished(t *testing.T, s *Service) {
	t.Helper()
	s.mu.Lock()
	done := s.connectDone
	s.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("connection setup did not finish")
		}
	}
}

func TestRememberedPasswordsSurviveReloadWithoutEnteringWorkspace(t *testing.T) {
	for _, password := range []string{"", "private-fixture-password"} {
		t.Run(map[bool]string{true: "empty", false: "nonempty"}[password == ""], func(t *testing.T) {
			calls := make(chan string, 3)
			connector := connectorFunc(func(_ context.Context, _ ServerProfile, value string, update func(RemoteState)) (RemoteConnection, error) {
				calls <- value
				update(channelState("1"))
				return &fakeConnection{}, nil
			})
			s, passwords, profiles := passwordService(t, connector)
			if _, err := s.ConnectServerWithPassword("one", password, true); err != nil {
				t.Fatal(err)
			}
			if got := <-calls; got != password {
				t.Fatal("connector received different credential")
			}
			waitForMode(t, s, "connected")
			waitConnectFinished(t, s)
			status, err := s.GetServerCredentialStatus("one")
			if err != nil || !status.Saved || !status.Remember {
				t.Fatalf("status %+v %v", status, err)
			}
			state, _ := s.GetWorkspace()
			encoded, _ := json.Marshal(state)
			bookmarks, _ := json.Marshal(profiles.profiles)
			if password != "" && (strings.Contains(string(encoded), password) || strings.Contains(string(bookmarks), password)) {
				t.Fatal("password escaped secret store")
			}
			if _, err = s.DisconnectServer(); err != nil {
				t.Fatal(err)
			}
			reloaded, err := NewWithPasswordStore(profiles, connector, passwords)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(reloaded.Shutdown)
			if _, err = reloaded.ConnectSavedServer("one"); err != nil {
				t.Fatal(err)
			}
			if got := <-calls; got != password {
				t.Fatal("saved connection lost its credential")
			}
			waitForMode(t, reloaded, "connected")
			if _, err = reloaded.ConnectSavedServer("one"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-calls:
				t.Fatal("double activation reconnected the same server")
			default:
			}
		})
	}
}

func TestWrongPasswordDoesNotOverwriteSavedPassword(t *testing.T) {
	s, passwords, profiles := passwordService(t, connectorFunc(func(context.Context, ServerProfile, string, func(RemoteState)) (RemoteConnection, error) {
		return nil, errors.New("authentication failed")
	}))
	key := passwordKey(profiles.profiles[0])
	_ = passwords.Set(key, "last-good")
	if _, err := s.ConnectServerWithPassword("one", "wrong", true); err != nil {
		t.Fatal(err)
	}
	waitForMode(t, s, "failed")
	waitConnectFinished(t, s)
	got, _ := passwords.Get(key)
	if got != "last-good" {
		t.Fatal("failed authentication overwrote known credential")
	}
}

func TestCredentialFailurePreservesConnectionAndSwitchingIsOrdered(t *testing.T) {
	first := &fakeConnection{}
	calls := make(chan string, 3)
	s, passwords, profiles := passwordService(t, connectorFunc(func(_ context.Context, p ServerProfile, _ string, update func(RemoteState)) (RemoteConnection, error) {
		calls <- p.ID
		update(channelState("1"))
		if p.ID == "one" {
			return first, nil
		}
		if first.closed.Load() != 1 {
			t.Error("new server connected before old connection closed")
		}
		return &fakeConnection{}, nil
	}))
	if _, err := s.ConnectServerWithPassword("one", "", false); err != nil {
		t.Fatal(err)
	}
	<-calls
	waitForMode(t, s, "connected")
	waitConnectFinished(t, s)
	passwords.getErr = errors.New("private error")
	if _, err := s.ConnectSavedServer("two"); err == nil {
		t.Fatal("missing credential accepted")
	}
	state, _ := s.GetWorkspace()
	if first.closed.Load() != 0 || state.Session.ServerID != "one" {
		t.Fatal("credential failure disconnected existing session")
	}
	passwords.getErr = nil
	_ = passwords.Set(passwordKey(profiles.profiles[1]), "next")
	if _, err := s.ConnectSavedServer("two"); err != nil {
		t.Fatal(err)
	}
	if <-calls != "two" {
		t.Fatal("wrong switch destination")
	}
	waitForMode(t, s, "connected")
}

func TestRememberFailureKeepsOnlineAndExposesNoPrivateDetails(t *testing.T) {
	s, passwords, _ := passwordService(t, connectorFunc(func(_ context.Context, _ ServerProfile, _ string, update func(RemoteState)) (RemoteConnection, error) {
		update(channelState("1"))
		return &fakeConnection{}, nil
	}))
	passwords.setErr = errors.New("sensitive-native-error")
	if _, err := s.ConnectServerWithPassword("one", "secret", true); err != nil {
		t.Fatal(err)
	}
	waitForMode(t, s, "connected")
	waitConnectFinished(t, s)
	state, _ := s.GetWorkspace()
	if state.Session.Mode != "connected" || state.Session.CredentialError == "" || strings.Contains(state.Session.CredentialError, "sensitive") {
		t.Fatalf("bad persistence error: %+v", state.Session)
	}
}

func TestPasswordPreferenceForgetAndDestinationChanges(t *testing.T) {
	s, passwords, profiles := passwordService(t, connectorFunc(func(_ context.Context, _ ServerProfile, _ string, update func(RemoteState)) (RemoteConnection, error) {
		update(channelState("1"))
		return &fakeConnection{}, nil
	}))
	profile := profiles.profiles[0]
	key := passwordKey(profile)
	_ = passwords.Set(key, "saved")
	profile.Name = "renamed"
	profile.Nickname = "new nickname"
	if _, err := s.SaveServer(profile); err != nil {
		t.Fatal(err)
	}
	if found, _ := passwords.Has(key); !found {
		t.Fatal("rename lost password")
	}
	profile.Address = "elsewhere.invalid"
	if _, err := s.SaveServer(profile); err != nil {
		t.Fatal(err)
	}
	if found, _ := passwords.Has(key); found {
		t.Fatal("address change retained old password")
	}
	_ = passwords.Set(passwordKey(profile), "replacement")
	if _, err := s.ForgetServerPassword("one"); err != nil {
		t.Fatal(err)
	}
	status, _ := s.GetServerCredentialStatus("one")
	if status.Saved || status.Remember {
		t.Fatal("forget did not clear password preference")
	}
	_ = passwords.Set(passwordKey(profile), "replacement")
	if _, err := s.ConnectServerWithPassword("one", "once", false); err != nil {
		t.Fatal(err)
	}
	waitForMode(t, s, "connected")
	waitConnectFinished(t, s)
	if found, _ := passwords.Has(passwordKey(profile)); found {
		t.Fatal("single-use connection retained password")
	}
	_, _ = s.DisconnectServer()
	_ = passwords.Set(passwordKey(profile), "replacement")
	if _, err := s.DeleteServer("one"); err != nil {
		t.Fatal(err)
	}
	if found, _ := passwords.Has(passwordKey(profile)); found {
		t.Fatal("deleted bookmark retained password")
	}
}

func TestCredentialCleanupFailurePreservesBookmark(t *testing.T) {
	s, passwords, profiles := passwordService(t, nil)
	passwords.deleteErr = errors.New("locked")
	profile := profiles.profiles[0]
	profile.Address = "changed.invalid"
	if _, err := s.SaveServer(profile); err == nil {
		t.Fatal("changed address despite failed cleanup")
	}
	if _, err := s.DeleteServer("one"); err == nil {
		t.Fatal("deleted bookmark despite failed cleanup")
	}
	state, _ := s.GetWorkspace()
	if len(state.Servers) != 2 || state.Servers[0].Address == profile.Address {
		t.Fatal("failed cleanup altered bookmarks")
	}
}

type gatedCloseConnection struct {
	fakeConnection
	entered, release chan struct{}
}

func (c *gatedCloseConnection) Close() error {
	close(c.entered)
	<-c.release
	return c.fakeConnection.Close()
}

func TestDestinationEditDuringSwitchNeverReceivesOldPassword(t *testing.T) {
	old := &gatedCloseConnection{entered: make(chan struct{}), release: make(chan struct{})}
	s, passwords, profiles := passwordService(t, connectorFunc(func(_ context.Context, p ServerProfile, _ string, update func(RemoteState)) (RemoteConnection, error) {
		if p.ID != "one" {
			t.Error("credential was sent after the destination changed")
		}
		update(channelState("1"))
		return old, nil
	}))
	if _, err := s.ConnectServerWithPassword("one", "", false); err != nil {
		t.Fatal(err)
	}
	waitForMode(t, s, "connected")
	waitConnectFinished(t, s)
	target := profiles.profiles[1]
	_ = passwords.Set(passwordKey(target), "bound-to-original-destination")
	result := make(chan error, 1)
	go func() { _, err := s.ConnectSavedServer("two"); result <- err }()
	<-old.entered
	target.Address = "different.invalid"
	_, saveErr := s.SaveServer(target)
	close(old.release)
	if saveErr != nil {
		t.Fatal(saveErr)
	}
	if err := <-result; err == nil {
		t.Fatal("switch accepted a changed destination")
	}
}
