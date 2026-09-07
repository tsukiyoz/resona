package ts3

import (
	"context"
	"errors"
	"fmt"
	"time"

	teamspeak "github.com/honeybbq/teamspeak-go"
)

const memberSubscriptionTimeout = 8 * time.Second

// Subscription expands the server-pushed member view within this identity's
// permissions. It must never wait for a command response in the packet observer.
func (s *connection) startMemberSubscription() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.state.state.Closed || s.subscribeStarted || !s.state.ready() {
		return
	}
	s.subscribeStarted = true
	s.state.state.MemberSyncState = "pending"
	s.state.state.MemberSyncError = ""
	s.publishMemberSyncLocked()
	s.background.Add(1)
	go func() {
		defer s.background.Done()
		ctx, cancel := context.WithTimeout(s.lifetime, memberSubscriptionTimeout)
		defer cancel()
		s.subscribeMembers(ctx)
	}()
}

func (s *connection) subscribeMembers(ctx context.Context) {
	err := s.execCommand(ctx, "channelsubscribeall")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.state.state.Closed || s.lifetime.Err() != nil {
		return
	}
	s.state.state.MemberSyncState = "ready"
	s.state.state.MemberSyncError = ""
	if err != nil {
		s.state.state.MemberSyncState = "limited"
		s.state.state.MemberSyncError = memberSubscriptionError(err)
	}
	s.publishMemberSyncLocked()
}

func (s *connection) publishMemberSyncLocked() {
	snapshot := s.state.snapshot()
	// A status-only update must not repeat the last protocol notification.
	snapshot.Events = nil
	snapshot.Messages = nil
	s.update(snapshot)
}

func memberSubscriptionError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "成员订阅超时，当前仅显示服务器已推送的成员；可重新连接后重试"
	}
	var commandError *teamspeak.CommandError
	if errors.As(err, &commandError) {
		return fmt.Sprintf("成员订阅未完成（错误码 %d），当前仅显示服务器已推送的成员", commandError.ID)
	}
	return "成员订阅未完成，当前仅显示服务器已推送的成员；可重新连接后重试"
}
