package ts3

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	teamspeak "github.com/honeybbq/teamspeak-go"
	"github.com/honeybbq/teamspeak-go/commands"
	"github.com/tsukiyoz/resona/internal/client"
)

// SendChannelMessage serializes with local moves. TS3 ignores target for channel
// messages, so an administrator moving us concurrently remains a server race.
func (s *connection) SendChannelMessage(ctx context.Context, channelID, text string) error {
	if len(text) > client.MaxChannelMessageBytes {
		return client.ErrMessageTooLong
	}
	if !utf8.ValidString(text) || strings.TrimSpace(text) == "" || strings.ContainsRune(text, 0) {
		return &client.MessageSendError{Message: "消息不能为空，且必须是有效文本"}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.lifetime, cancel)
	defer stop()
	select {
	case s.commandGate <- struct{}{}:
		defer func() { <-s.commandGate }()
	case <-ctx.Done():
		return messageNotSent(ctx.Err())
	}
	if err := ctx.Err(); err != nil {
		return messageNotSent(err)
	}
	s.mu.Lock()
	err := s.checkTextChannelLocked(channelID)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	raw := commands.BuildCommand("sendtextmessage", map[string]string{
		"targetmode": "2", "target": channelID, "msg": text,
	})
	if err := s.execCommand(ctx, raw); err != nil {
		return channelTextSendError(err)
	}
	return nil
}

// This runs after the upstream throttle, immediately before packet dispatch.
// Serialize whole commands so a long text's fragments cannot interleave with
// subscription or client-update packets. No acknowledgment is awaited here.
func (s *connection) guardChannelText(next func(string) error) func(string) error {
	return func(raw string) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		command := commands.ParseCommand(raw)
		if command == nil || command.Name != "sendtextmessage" || command.Params["targetmode"] != "2" {
			return next(raw)
		}
		if err := s.checkTextChannelLocked(command.Params["target"]); err != nil {
			return err
		}
		return next(raw)
	}
}

func (s *connection) checkTextChannelLocked(channelID string) error {
	if s.closing || s.state.state.Closed || s.lifetime.Err() != nil || !s.state.ready() {
		return client.ErrMessageUnavailable
	}
	if _, exists := s.state.channels[channelID]; !exists || !validID(channelID) {
		return client.ErrMessageUnavailable
	}
	if s.state.users[s.state.state.SelfID].ChannelID != channelID {
		return client.ErrMessageChannelChanged
	}
	return nil
}

func messageNotSent(cause error) error {
	return errors.Join(&client.MessageSendError{Message: "消息未发送，操作已取消"}, cause)
}

func channelTextSendError(err error) error {
	for _, rejected := range []error{client.ErrMessageUnavailable, client.ErrMessageChannelChanged} {
		if errors.Is(err, rejected) {
			return rejected
		}
	}
	var commandError *teamspeak.CommandError
	if errors.As(err, &commandError) {
		switch commandError.ID {
		case 0x0a08:
			return client.ErrMessagePermissionDenied
		case 0x0605:
			return client.ErrMessageTooLong
		case 0x020c:
			return client.ErrMessageRateLimited
		case 0x0300, 0x0702:
			return client.ErrMessageUnavailable
		default:
			return &client.MessageSendError{Message: fmt.Sprintf("服务器拒绝发送消息（错误码 %d）", commandError.ID)}
		}
	}
	var cancellation error
	if errors.Is(err, context.DeadlineExceeded) {
		cancellation = context.DeadlineExceeded
	} else if errors.Is(err, context.Canceled) {
		cancellation = context.Canceled
	}
	return errors.Join(&client.MessageSendError{Message: "发送结果未确认，请先检查频道记录", Uncertain: true}, client.ErrMessageDeliveryUnknown, cancellation)
}
