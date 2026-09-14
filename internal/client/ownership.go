package client

import (
	"context"
	"errors"
	"time"
)

type OwnerClaimer interface {
	ClaimOwner(context.Context, string) error
}

func (s *Service) ClaimServerOwner(sessionID, token string) (Workspace, error) {
	return s.ClaimServerOwnerContext(context.Background(), sessionID, token)
}

func (s *Service) ClaimServerOwnerContext(ctx context.Context, sessionID, token string) (Workspace, error) {
	s.mu.Lock()
	if s.shutdown || sessionID == "" || s.state.Session.ID != sessionID || s.state.Session.Mode != "connected" || !s.state.Session.CanClaimOwner {
		s.mu.Unlock()
		return Workspace{}, errors.New("当前会话不可认领所有者")
	}
	remote, ok := s.connection.(OwnerClaimer)
	s.mu.Unlock()
	if !ok {
		return Workspace{}, errors.New("当前协议不支持所有者认领")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := remote.ClaimOwner(ctx, token); err != nil {
		return Workspace{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Session.ID != sessionID || s.shutdown {
		return Workspace{}, errors.New("会话已改变，请重新查看服务器角色")
	}
	return s.snapshot(), nil
}
