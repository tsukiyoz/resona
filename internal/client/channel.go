package client

import (
	"context"
	"errors"
)

func (s *Service) selectRemoteChannelLocked(id string) (Workspace, error) {
	if s.shutdown || s.connection == nil {
		return Workspace{}, errors.New("服务器连接已经关闭")
	}
	if s.state.Session.SwitchingChannelID != "" {
		return Workspace{}, errors.New("正在切换频道，请稍候")
	}
	var target *Channel
	for i := range s.state.Channels {
		if s.state.Channels[i].ID == id {
			target = &s.state.Channels[i]
			break
		}
	}
	if target == nil {
		return Workspace{}, errors.New("频道不存在")
	}
	if target.PasswordRequired {
		return Workspace{}, ErrChannelPasswordRequired
	}
	if s.state.Session.ChannelID == id {
		return s.snapshot(), nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.moveTimeout)
	s.moveCancel = cancel
	s.moveSequence++
	sequence, generation := s.moveSequence, s.generation
	connection := s.connection
	s.state.Session.SwitchingChannelID = id
	s.state.Session.Error = ""
	s.cleanupWG.Add(1)
	go func() {
		defer s.cleanupWG.Done()
		defer cancel()
		err := connection.MoveChannel(ctx, id)
		if err == nil {
			err = ctx.Err()
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.generation != generation || s.moveSequence != sequence || s.state.Session.Mode != "connected" {
			return
		}
		s.moveCancel = nil
		s.state.Session.SwitchingChannelID = ""
		if err != nil {
			s.state.Session.Error = channelMoveError(err)
		}
	}()
	return s.snapshot(), nil
}

func (s *Service) cancelMoveLocked() {
	if s.moveCancel != nil {
		s.moveCancel()
		s.moveCancel = nil
	}
	s.moveSequence++
	s.state.Session.SwitchingChannelID = ""
}

func channelMoveError(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "切换频道超时，请稍后重试"
	case errors.Is(err, ErrChannelPermissionDenied):
		return ErrChannelPermissionDenied.Error()
	case errors.Is(err, ErrChannelPasswordRequired):
		return ErrChannelPasswordRequired.Error()
	default:
		return "切换频道失败，请稍后重试"
	}
}
