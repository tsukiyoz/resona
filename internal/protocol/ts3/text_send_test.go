package ts3

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	teamspeak "github.com/honeybbq/teamspeak-go"
	"github.com/honeybbq/teamspeak-go/commands"
	"github.com/tsukiyoz/resona/internal/client"
)

func TestChannelTextSendEscapesOnePlainMessage(t *testing.T) {
	text := "  [b]literal[/b] <img src=x>\\s | 中文\nnext line  "
	var sent string
	s := newMoveTestConnection(t, func(_ context.Context, raw string) error { sent = raw; return nil })
	if err := s.SendChannelMessage(context.Background(), "1", text); err != nil {
		t.Fatal(err)
	}
	command := commands.ParseCommand(sent)
	if command.Name != "sendtextmessage" || command.Params["targetmode"] != "2" || command.Params["target"] != "1" || command.Params["msg"] != text {
		t.Fatalf("incorrect channel message: %v", command)
	}
	if strings.ContainsAny(sent, "\n\r|") {
		t.Fatal("unescaped delimiter could create another command or row")
	}
}

func TestChannelTextSendValidatesBytesAndDestinationBeforeDispatch(t *testing.T) {
	for _, tc := range []struct {
		name, channel, text string
	}{
		{"different channel", "2", "message"},
		{"unknown channel", "900", "message"},
		{"invalid channel", "1 msg=bad", "message"},
		{"empty", "1", "  \n\t"},
		{"null", "1", "before\x00after"},
		{"invalid utf8", "1", string([]byte{0xff})},
		{"ascii too long", "1", strings.Repeat("x", client.MaxChannelMessageBytes+1)},
		{"utf8 too long", "1", strings.Repeat("中", client.MaxChannelMessageBytes/3+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newMoveTestConnection(t, func(context.Context, string) error { t.Fatal("invalid message dispatched"); return nil })
			if err := s.SendChannelMessage(context.Background(), tc.channel, tc.text); err == nil {
				t.Fatal("invalid message was accepted")
			}
		})
	}
	s := newMoveTestConnection(t, func(context.Context, string) error { return nil })
	for _, text := range []string{strings.Repeat("x", client.MaxChannelMessageBytes), strings.Repeat("中", client.MaxChannelMessageBytes/3)} {
		if err := s.SendChannelMessage(context.Background(), "1", text); err != nil {
			t.Fatalf("valid byte-length message rejected: %v", err)
		}
	}
}

func TestChannelTextWaitsForMoveThenRejectsStaleDestination(t *testing.T) {
	moveSent, moveAck := make(chan struct{}), make(chan struct{})
	s := newMoveTestConnection(t, func(ctx context.Context, raw string) error {
		if commands.ParseCommand(raw).Name != "clientmove" {
			t.Error("message dispatched after its channel changed")
			return nil
		}
		close(moveSent)
		select {
		case <-moveAck:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	moved, textResult := make(chan error, 1), make(chan error, 1)
	go func() { moved <- s.MoveChannel(ctx, "2") }()
	<-moveSent
	go func() { textResult <- s.SendChannelMessage(ctx, "1", "old channel") }()
	observeMove(s, "2")
	close(moveAck)
	if err := <-moved; err != nil {
		t.Fatal(err)
	}
	if err := <-textResult; !errors.Is(err, client.ErrMessageChannelChanged) {
		t.Fatalf("stale message result: %v", err)
	}
}

func TestMoveWaitsUntilChannelTextAcknowledged(t *testing.T) {
	sent, ack := make(chan struct{}), make(chan struct{})
	moveSent := make(chan struct{})
	var s *connection
	s = newMoveTestConnection(t, func(ctx context.Context, raw string) error {
		if commands.ParseCommand(raw).Name == "clientmove" {
			close(moveSent)
			observeMove(s, "2")
			return nil
		}
		close(sent)
		select {
		case <-ack:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	moved, textResult := make(chan error, 1), make(chan error, 1)
	go func() { textResult <- s.SendChannelMessage(ctx, "1", "before move") }()
	<-sent
	go func() { moved <- s.MoveChannel(ctx, "2") }()
	select {
	case <-moveSent:
		t.Fatal("move raced ahead of the message acknowledgment")
	case <-time.After(20 * time.Millisecond):
	}
	close(ack)
	if err := <-textResult; err != nil {
		t.Fatal(err)
	}
	if err := <-moved; err != nil {
		t.Fatal(err)
	}
}

func TestChannelTextGuardRechecksAfterThrottle(t *testing.T) {
	var s *connection
	s = newMoveTestConnection(t, func(_ context.Context, raw string) error {
		// Simulate a server move while the upstream throttle is waiting.
		observeMove(s, "2")
		return s.guardChannelText(func(string) error { t.Fatal("stale-channel text reached packet dispatch"); return nil })(raw)
	})
	if err := s.SendChannelMessage(context.Background(), "1", "old channel"); !errors.Is(err, client.ErrMessageChannelChanged) {
		t.Fatalf("guard did not reject stale destination: %v", err)
	}
	var passthrough string
	raw := "clientupdate client_input_muted=1"
	if err := s.guardChannelText(func(value string) error { passthrough = value; return nil })(raw); err != nil || passthrough != raw {
		t.Fatalf("guard changed an unrelated command: %v", err)
	}
}

func TestChannelTextGuardKeepsCommandFragmentsTogether(t *testing.T) {
	s := newMoveTestConnection(t, nil)
	started, finishText := make(chan struct{}), make(chan struct{})
	otherDispatched := make(chan struct{})
	dispatch := s.guardChannelText(func(raw string) error {
		if commands.ParseCommand(raw).Name == "sendtextmessage" {
			close(started)
			<-finishText
		} else {
			close(otherDispatched)
		}
		return nil
	})
	textDone, otherDone := make(chan error, 1), make(chan error, 1)
	go func() { textDone <- dispatch("sendtextmessage targetmode=2 target=1 msg=long") }()
	<-started
	go func() { otherDone <- dispatch("channelsubscribeall") }()
	select {
	case <-otherDispatched:
		t.Error("another command interleaved with text packet dispatch")
	case <-time.After(20 * time.Millisecond):
	}
	close(finishText)
	if err := <-textDone; err != nil {
		t.Fatal(err)
	}
	if err := <-otherDone; err != nil {
		t.Fatal(err)
	}
}

func TestChannelTextCancellationDistinguishesDispatchBoundary(t *testing.T) {
	s := newMoveTestConnection(t, func(context.Context, string) error { t.Fatal("canceled message dispatched"); return nil })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := s.SendChannelMessage(ctx, "1", "canceled")
	var sendError *client.MessageSendError
	if !errors.Is(err, context.Canceled) || !errors.As(err, &sendError) || sendError.Uncertain {
		t.Fatalf("before-dispatch cancellation must be certain: %v", err)
	}
	sent := make(chan struct{})
	s = newMoveTestConnection(t, func(ctx context.Context, _ string) error { close(sent); <-ctx.Done(); return ctx.Err() })
	result := make(chan error, 1)
	go func() { result <- s.SendChannelMessage(context.Background(), "1", "pending acknowledgment") }()
	<-sent
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-result:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, client.ErrMessageDeliveryUnknown) || !errors.As(err, &sendError) || !sendError.Uncertain {
			t.Fatalf("after-dispatch cancellation must be unknown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel the text operation")
	}
}

func TestChannelTextServerErrorsAreExplicitAndSanitized(t *testing.T) {
	for _, tc := range []struct {
		code uint32
		want error
	}{
		{0x0a08, client.ErrMessagePermissionDenied},
		{0x0605, client.ErrMessageTooLong},
		{0x020c, client.ErrMessageRateLimited},
		{0x0300, client.ErrMessageUnavailable},
	} {
		err := channelTextSendError(&teamspeak.CommandError{ID: tc.code, Message: "private server text"})
		if !errors.Is(err, tc.want) || strings.Contains(err.Error(), "private") {
			t.Fatalf("bad mapped rejection: %v", err)
		}
	}
	err := channelTextSendError(&teamspeak.CommandError{ID: 12345, Message: "private server text"})
	var sendError *client.MessageSendError
	if !errors.As(err, &sendError) || sendError.Uncertain || !strings.Contains(sendError.Message, "12345") || strings.Contains(err.Error(), "private") {
		t.Fatalf("bad unmapped server rejection: %v", err)
	}
	for _, cause := range []error{context.DeadlineExceeded, errors.New("private transport detail")} {
		err := channelTextSendError(cause)
		if !errors.Is(err, client.ErrMessageDeliveryUnknown) || !errors.As(err, &sendError) || !sendError.Uncertain || strings.Contains(err.Error(), "private") {
			t.Fatalf("bad unknown delivery error: %v", err)
		}
		if cause == context.DeadlineExceeded && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("deadline identity lost")
		}
	}
}
