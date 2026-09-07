package ts3

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	teamspeak "github.com/honeybbq/teamspeak-go"
	"github.com/honeybbq/teamspeak-go/commands"
	"github.com/honeybbq/teamspeak-go/crypto"
	"github.com/tsukiyoz/resona/internal/client"
)

func newMoveTestConnection(t *testing.T, exec func(context.Context, string) error) *connection {
	t.Helper()
	lifetime, stop := context.WithCancel(context.Background())
	id, err := crypto.GenerateIdentity(0)
	if err != nil {
		t.Fatal(err)
	}
	s := &connection{state: newReducer("uid"), update: func(client.RemoteState) {}, ready: make(chan struct{}), ended: make(chan struct{}), changed: make(chan struct{}), commandGate: make(chan struct{}, 1), lifetime: lifetime, stop: stop, execCommand: exec,
		client: teamspeak.NewClient(id, "127.0.0.1:9987", "test", teamspeak.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))}
	s.state.state.SelfID = "7"
	s.state.initialized, s.state.listed = true, true
	s.state.users["7"] = client.User{ID: "7", ChannelID: "1"}
	s.state.channels["1"] = client.Channel{ID: "1"}
	s.state.channels["2"] = client.Channel{ID: "2"}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func observeMove(s *connection, channel string) {
	s.observe(teamspeak.IncomingCommand{Name: "notifyclientmoved", Params: map[string]string{"clid": "7", "ctid": channel}})
}

func TestMoveWaitsForAcknowledgmentAndOwnEvent(t *testing.T) {
	for _, eventFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "event first", false: "ack first"}[eventFirst], func(t *testing.T) {
			sent, ack := make(chan string, 1), make(chan struct{})
			s := newMoveTestConnection(t, func(ctx context.Context, raw string) error {
				sent <- raw
				select {
				case <-ack:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- s.MoveChannel(ctx, "2") }()
			cmd := commands.ParseCommand(<-sent)
			if cmd.Name != "clientmove" || cmd.Params["clid"] != "7" || cmd.Params["cid"] != "2" {
				t.Fatalf("wrong own move command: %v", cmd)
			}
			if eventFirst {
				observeMove(s, "2")
			} else {
				close(ack)
			}
			select {
			case err := <-result:
				t.Fatalf("finished before both signals: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
			if eventFirst {
				close(ack)
			} else {
				observeMove(s, "2")
			}
			if err := <-result; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMoveCloseCancelsPendingCommand(t *testing.T) {
	sent := make(chan struct{})
	s := newMoveTestConnection(t, func(ctx context.Context, _ string) error { close(sent); <-ctx.Done(); return ctx.Err() })
	result := make(chan error, 1)
	go func() { result <- s.MoveChannel(context.Background(), "2") }()
	<-sent
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not cancel move")
	}
}

func TestMoveErrorsDoNotExposeServerText(t *testing.T) {
	for _, tc := range []struct {
		code uint32
		want error
	}{{0x0a08, client.ErrChannelPermissionDenied}, {0x030d, client.ErrChannelPasswordRequired}} {
		s := newMoveTestConnection(t, func(context.Context, string) error {
			return &teamspeak.CommandError{ID: tc.code, Message: "private server text"}
		})
		if err := s.MoveChannel(context.Background(), "2"); !errors.Is(err, tc.want) {
			t.Fatalf("code %d: %v", tc.code, err)
		}
		if s.state.snapshot().ChannelID != "1" {
			t.Fatal("failed command moved local state")
		}
	}
}

func TestMoveRejectsKnownPasswordBeforeSending(t *testing.T) {
	s := newMoveTestConnection(t, func(context.Context, string) error { t.Fatal("sent password channel command"); return nil })
	s.state.channels["2"] = client.Channel{ID: "2", PasswordRequired: true}
	if err := s.MoveChannel(context.Background(), "2"); !errors.Is(err, client.ErrChannelPasswordRequired) {
		t.Fatal(err)
	}
}
