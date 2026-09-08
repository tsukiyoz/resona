package client

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Optional fields distinguish unavailable data from a server-reported zero/false.
type ChannelDetails struct {
	ID                        string  `json:"id"`
	Name                      string  `json:"name"`
	Topic                     *string `json:"topic,omitempty"`
	Description               *string `json:"description,omitempty"`
	Codec                     *string `json:"codec,omitempty"`
	CodecQuality              *int    `json:"codecQuality,omitempty"`
	Permanent                 *bool   `json:"permanent,omitempty"`
	SemiPermanent             *bool   `json:"semiPermanent,omitempty"`
	Default                   *bool   `json:"default,omitempty"`
	PasswordRequired          *bool   `json:"passwordRequired,omitempty"`
	MaxClients                *int    `json:"maxClients,omitempty"`
	MaxClientsUnlimited       *bool   `json:"maxClientsUnlimited,omitempty"`
	MaxFamilyClients          *int    `json:"maxFamilyClients,omitempty"`
	MaxFamilyClientsUnlimited *bool   `json:"maxFamilyClientsUnlimited,omitempty"`
	MaxFamilyClientsInherited *bool   `json:"maxFamilyClientsInherited,omitempty"`
	Members                   int     `json:"members"`
	MemberSyncState           string  `json:"memberSyncState"`
}

type UserDetails struct {
	ID          string  `json:"id"`
	Nickname    string  `json:"nickname"`
	ChannelID   string  `json:"channelID"`
	Description *string `json:"description,omitempty"`
	IdentityUID *string `json:"identityUID,omitempty"`
	Away        *bool   `json:"away,omitempty"`
	AwayMessage *string `json:"awayMessage,omitempty"`
	InputMuted  *bool   `json:"inputMuted,omitempty"`
	OutputMuted *bool   `json:"outputMuted,omitempty"`
}

type IconResource struct {
	Ref     string `json:"ref"`
	DataURL string `json:"dataURL"`
}

// Readers must honor cancellation and stop work when their connection closes.
type ChannelDetailsReader interface {
	ReadChannelDetails(context.Context, string) (ChannelDetails, error)
}
type UserDetailsReader interface {
	ReadUserDetails(context.Context, string) (UserDetails, error)
}
type IconResourceReader interface {
	ReadIconResource(context.Context, string) (IconResource, error)
}

func (s *Service) GetChannelDetails(ctx context.Context, sessionID, id string) (ChannelDetails, error) {
	ctx, connection, valid, finish, err := s.beginRead(ctx, sessionID)
	if err != nil {
		return ChannelDetails{}, err
	}
	defer finish()
	reader, ok := connection.(ChannelDetailsReader)
	if !ok {
		return ChannelDetails{}, errors.New("当前连接不支持频道详情")
	}
	result, err := reader.ReadChannelDetails(ctx, id)
	if !valid() {
		return ChannelDetails{}, context.Canceled
	}
	if ctx.Err() != nil {
		return ChannelDetails{}, ctx.Err()
	}
	if err != nil {
		return ChannelDetails{}, err
	}
	return result, nil
}

func (s *Service) GetUserDetails(ctx context.Context, sessionID, id string) (UserDetails, error) {
	ctx, connection, valid, finish, err := s.beginRead(ctx, sessionID)
	if err != nil {
		return UserDetails{}, err
	}
	defer finish()
	reader, ok := connection.(UserDetailsReader)
	if !ok {
		return UserDetails{}, errors.New("当前连接不支持用户详情")
	}
	result, err := reader.ReadUserDetails(ctx, id)
	if !valid() {
		return UserDetails{}, context.Canceled
	}
	if ctx.Err() != nil {
		return UserDetails{}, ctx.Err()
	}
	if err != nil {
		return UserDetails{}, err
	}
	return result, nil
}

func (s *Service) GetIconResource(ctx context.Context, sessionID, ref string) (IconResource, error) {
	ctx, connection, valid, finish, err := s.beginRead(ctx, sessionID)
	if err != nil {
		return IconResource{}, err
	}
	defer finish()
	reader, ok := connection.(IconResourceReader)
	if !ok {
		return IconResource{}, errors.New("当前连接不支持图标资源")
	}
	result, err := reader.ReadIconResource(ctx, ref)
	if !valid() {
		return IconResource{}, context.Canceled
	}
	if ctx.Err() != nil {
		return IconResource{}, ctx.Err()
	}
	if err != nil {
		return IconResource{}, err
	}
	return result, nil
}

// Observe the existing invalidation stream so disconnect cancels readers even
// when an adapter is still waiting for its Close call during voice cleanup.
func (s *Service) beginRead(parent context.Context, sessionID string) (context.Context, RemoteConnection, func() bool, func(), error) {
	s.mu.Lock()
	if s.shutdown || s.state.Session.Mode != "connected" || s.state.Session.ID != sessionID || s.connection == nil {
		s.mu.Unlock()
		return nil, nil, nil, nil, errors.New("服务器会话已更改或未连接")
	}
	if s.activeReads >= 4 {
		s.mu.Unlock()
		return nil, nil, nil, nil, errors.New("详情或资源请求过多，请稍后重试")
	}
	s.activeReads++
	generation, connection := s.generation, s.connection
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	valid := func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return !s.shutdown && s.generation == generation && s.state.Session.Mode == "connected" && s.state.Session.ID == sessionID
	}
	changes, unsubscribe := s.SubscribeChanges()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-changes:
				if !valid() {
					cancel()
					return
				}
			}
		}
	}()
	var once sync.Once
	finish := func() {
		once.Do(func() {
			cancel()
			<-done
			unsubscribe()
			s.mu.Lock()
			s.activeReads--
			s.mu.Unlock()
		})
	}
	return ctx, connection, valid, finish, nil
}
