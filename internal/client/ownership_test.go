package client

import (
	"context"
	"testing"
)

type claimConnection struct {
	fakeConnection
	called int
	claim  func()
}

func (c *claimConnection) ClaimOwner(context.Context, string) error {
	c.called++
	if c.claim != nil {
		c.claim()
	}
	return nil
}

func TestOwnerClaimRejectsStaleAndUnsupportedSessions(t *testing.T) {
	c := &claimConnection{}
	s := &Service{connection: c, state: Workspace{Session: Session{ID: "current", Mode: "connected", CanClaimOwner: true}}}
	if _, err := s.ClaimServerOwner("old", "test-token"); err == nil || c.called != 0 {
		t.Fatal("stale session dispatched")
	}
	s.state.Session.CanClaimOwner = false
	if _, err := s.ClaimServerOwner("current", "test-token"); err == nil || c.called != 0 {
		t.Fatal("unsupported claim dispatched")
	}
	s.state.Session.CanClaimOwner = true
	c.claim = func() { s.mu.Lock(); s.state.Session.ID = "new"; s.mu.Unlock() }
	if _, err := s.ClaimServerOwner("current", "test-token"); err == nil || c.called != 1 {
		t.Fatal("stale completion returned new session")
	}
}
