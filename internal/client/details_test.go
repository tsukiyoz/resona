package client

import (
	"context"
	"errors"
	"testing"
	"time"
)

type detailTestConnection struct {
	fakeConnection
	read func(context.Context, string) (ChannelDetails, error)
}

func (c *detailTestConnection) ReadChannelDetails(ctx context.Context, id string) (ChannelDetails, error) {
	return c.read(ctx, id)
}

func TestDetailsCancelOnDisconnectAndRejectOldSession(t *testing.T) {
	started := make(chan struct{})
	connection := &detailTestConnection{read: func(ctx context.Context, id string) (ChannelDetails, error) {
		close(started)
		<-ctx.Done()
		return ChannelDetails{ID: id}, nil
	}}
	s, _ := serviceWithProfile(t, nil)
	s.connection = connection
	s.state.Session = Session{ID: "old", Mode: "connected"}
	done := make(chan error, 1)
	go func() { _, err := s.GetChannelDetails(context.Background(), "old", "10"); done <- err }()
	<-started
	if _, err := s.DisconnectServer(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("late read accepted: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("disconnect did not cancel detail read")
	}
	if _, err := s.GetChannelDetails(context.Background(), "old", "10"); err == nil {
		t.Fatal("old session could read details")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeReads != 0 || len(s.subscribers) != 0 {
		t.Fatal("detail reader leaked bookkeeping")
	}
}

func TestReadConcurrencyIsBoundedAndReleased(t *testing.T) {
	s, _ := serviceWithProfile(t, nil)
	s.connection = &fakeConnection{}
	s.state.Session = Session{ID: "session", Mode: "connected"}
	var finishes []func()
	for range 4 {
		_, _, _, finish, err := s.beginRead(context.Background(), "session")
		if err != nil {
			t.Fatal(err)
		}
		finishes = append(finishes, finish)
	}
	if _, _, _, _, err := s.beginRead(context.Background(), "session"); err == nil {
		t.Fatal("concurrency limit exceeded")
	}
	for _, finish := range finishes {
		finish()
		finish()
	}
	_, _, _, finish, err := s.beginRead(context.Background(), "session")
	if err != nil {
		t.Fatalf("read capacity not released: %v", err)
	}
	finish()
}

func TestDetailReadPreservesCallerDeadline(t *testing.T) {
	s, _ := serviceWithProfile(t, nil)
	s.connection = &detailTestConnection{read: func(ctx context.Context, _ string) (ChannelDetails, error) {
		<-ctx.Done()
		return ChannelDetails{}, ctx.Err()
	}}
	s.state.Session = Session{ID: "session", Mode: "connected"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := s.GetChannelDetails(ctx, "session", "10"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost deadline error: %v", err)
	}
}
