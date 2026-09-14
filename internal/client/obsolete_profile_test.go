package client

import (
	"context"
	"testing"
)

func TestObsoleteBookmarkCannotReadCredentialsOrInterruptSession(t *testing.T) {
	calls := 0
	s, passwords, _ := passwordService(t, connectorFunc(func(context.Context, ServerProfile, string, func(RemoteState)) (RemoteConnection, error) {
		calls++
		return &fakeConnection{}, nil
	}))
	s.state.Session = Session{ID: "live", ServerID: "one", Mode: "connected"}
	s.state.Servers[1].Protocol = "unsupported"
	if _, err := s.ConnectSavedServer("two"); err == nil {
		t.Fatal("obsolete saved connection accepted")
	}
	if _, err := s.ConnectServerWithPassword("two", "secret", true); err == nil {
		t.Fatal("obsolete password connection accepted")
	}
	if _, err := s.GetServerCredentialStatus("two"); err == nil {
		t.Fatal("obsolete credential lookup accepted")
	}
	if calls != 0 || s.state.Session.ID != "live" || s.state.Session.Mode != "connected" {
		t.Fatal("obsolete bookmark interrupted session")
	}
	if len(passwords.values) != 0 {
		t.Fatal("obsolete bookmark saved credentials")
	}
	if passwords.reads != 0 {
		t.Fatal("obsolete bookmark accessed system credentials")
	}
}
