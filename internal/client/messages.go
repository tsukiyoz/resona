package client

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxChannelMessageBytes = 8192
const maxMessages = 500
const maxMessageBytes = 512 * 1024

var (
	ErrMessageChannelChanged   = errors.New("当前频道已改变，消息未发送")
	ErrMessagePermissionDenied = errors.New("没有在此频道发送消息的权限")
	ErrMessageTooLong          = errors.New("消息超过服务器允许的长度")
	ErrMessageRateLimited      = errors.New("发送过于频繁，请稍后重试")
	ErrMessageUnavailable      = errors.New("当前连接不支持发送频道消息")
	ErrMessageDeliveryUnknown  = errors.New("发送结果未确认，消息可能已送达")
)

type MessageSendError struct {
	Message   string
	Uncertain bool
}

func (e *MessageSendError) Error() string { return e.Message }

type ChannelMessageSender interface {
	SendChannelMessage(context.Context, string, string) error
}
type RemoteMessage struct{ ChannelID, UserID, Author, Text string }

func (s *Service) SendChannelMessage(sessionID, channelID, text string) (Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.notifyChangedLocked()
	if sessionID == "" || sessionID != s.state.Session.ID || channelID != s.state.Session.ChannelID {
		return Workspace{}, ErrMessageChannelChanged
	}
	if err := validateChannelText(text); err != nil {
		return Workspace{}, err
	}
	if err := s.canSendMessageLocked(); err != nil {
		return Workspace{}, err
	}
	message := Message{ID: rand.Text(), ChannelID: channelID, AuthorID: s.state.Session.SelfID, Author: s.state.Session.Nickname, Text: text, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Status: "sending"}
	s.state.Messages = append(s.state.Messages, message)
	s.trimMessagesLocked()
	s.startMessageLocked(message.ID)
	return s.snapshot(), nil
}

func (s *Service) RetryMessage(id string, allowDuplicate bool) (Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.notifyChangedLocked()
	if err := s.canSendMessageLocked(); err != nil {
		return Workspace{}, err
	}
	for i := range s.state.Messages {
		message := &s.state.Messages[i]
		if message.ID != id {
			continue
		}
		if message.ChannelID != s.state.Session.ChannelID {
			return Workspace{}, ErrMessageChannelChanged
		}
		if message.Status != "failed" && message.Status != "unconfirmed" {
			return Workspace{}, errors.New("此消息不能重试")
		}
		if message.Status == "unconfirmed" && !allowDuplicate {
			return Workspace{}, errors.New("上一条消息可能已送达，请确认是否重新发送")
		}
		message.Status, message.Error = "sending", ""
		s.startMessageLocked(id)
		return s.snapshot(), nil
	}
	return Workspace{}, errors.New("消息不存在")
}

func validateChannelText(text string) error {
	if !utf8.ValidString(text) || strings.TrimSpace(text) == "" || strings.ContainsRune(text, 0) {
		return errors.New("消息不能为空或包含无效字符")
	}
	if len(text) > MaxChannelMessageBytes {
		return ErrMessageTooLong
	}
	return nil
}

func (s *Service) canSendMessageLocked() error {
	if s.shutdown || s.state.Session.Mode != "connected" || s.connection == nil {
		return errors.New("当前未连接服务器")
	}
	if _, ok := s.connection.(ChannelMessageSender); !ok {
		return ErrMessageUnavailable
	}
	if s.state.Session.SwitchingChannelID != "" {
		return errors.New("正在切换频道，请等待服务器确认")
	}
	if s.state.Session.SendingMessageID != "" {
		return errors.New("上一条消息仍在发送")
	}
	for _, channel := range s.state.Channels {
		if channel.ID == s.state.Session.ChannelID && channel.Kind != "separator" {
			return nil
		}
	}
	return ErrMessageChannelChanged
}

func (s *Service) startMessageLocked(id string) {
	var outgoing Message
	for _, message := range s.state.Messages {
		if message.ID == id {
			outgoing = message
			break
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.messageTimeout)
	s.messageCancel = cancel
	s.messageSequence++
	sequence, generation := s.messageSequence, s.generation
	s.state.Session.SendingMessageID = id
	sender := s.connection.(ChannelMessageSender)
	s.cleanupWG.Add(1)
	go func() {
		defer s.cleanupWG.Done()
		defer cancel()
		err := sender.SendChannelMessage(ctx, outgoing.ChannelID, outgoing.Text)
		s.mu.Lock()
		defer s.mu.Unlock()
		defer s.notifyChangedLocked()
		if generation != s.generation || sequence != s.messageSequence {
			return
		}
		s.messageCancel = nil
		s.state.Session.SendingMessageID = ""
		for i := range s.state.Messages {
			message := &s.state.Messages[i]
			if message.ID != id {
				continue
			}
			message.Status, message.Error = messageResult(err)
			break
		}
	}()
}

func messageResult(err error) (string, string) {
	if err == nil {
		return "sent", ""
	}
	var failure *MessageSendError
	if errors.As(err, &failure) {
		if failure.Uncertain {
			return "unconfirmed", failure.Message
		}
		return "failed", failure.Message
	}
	for _, failure := range []error{ErrMessageChannelChanged, ErrMessagePermissionDenied, ErrMessageTooLong, ErrMessageRateLimited, ErrMessageUnavailable} {
		if errors.Is(err, failure) {
			return "failed", failure.Error()
		}
	}
	return "unconfirmed", ErrMessageDeliveryUnknown.Error()
}

func (s *Service) cancelMessageLocked() {
	if s.messageCancel != nil {
		s.messageCancel()
		s.messageCancel = nil
	}
	for i := range s.state.Messages {
		if s.state.Messages[i].ID == s.state.Session.SendingMessageID {
			s.state.Messages[i].Status = "unconfirmed"
			s.state.Messages[i].Error = ErrMessageDeliveryUnknown.Error()
		}
	}
	s.messageSequence++
	s.state.Session.SendingMessageID = ""
}

func (s *Service) appendRemoteMessagesLocked(messages []RemoteMessage) {
	for _, remote := range messages {
		if remote.UserID == "" || remote.UserID == s.state.Session.SelfID || remote.ChannelID == "" || remote.ChannelID != s.state.Session.ChannelID || !utf8.ValidString(remote.Text) || len(remote.Text) > MaxChannelMessageBytes {
			continue
		}
		s.state.Messages = append(s.state.Messages, Message{ID: rand.Text(), ChannelID: remote.ChannelID, AuthorID: remote.UserID, Author: remote.Author, Text: remote.Text, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Status: "received"})
	}
	s.trimMessagesLocked()
}

func (s *Service) trimMessagesLocked() {
	bytes := 0
	for _, message := range s.state.Messages {
		bytes += len(message.Text) + len(message.Author) + len(message.Error)
	}
	for len(s.state.Messages) > maxMessages || (bytes > maxMessageBytes && len(s.state.Messages) > 1) {
		index := 0
		if s.state.Messages[0].ID == s.state.Session.SendingMessageID {
			index = 1
		}
		message := s.state.Messages[index]
		bytes -= len(message.Text) + len(message.Author) + len(message.Error)
		copy(s.state.Messages[index:], s.state.Messages[index+1:])
		s.state.Messages[len(s.state.Messages)-1] = Message{}
		s.state.Messages = s.state.Messages[:len(s.state.Messages)-1]
	}
}
