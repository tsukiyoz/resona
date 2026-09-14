package client

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type ChannelManager interface {
	CreateChannel(context.Context, string, string) error
	UpdateChannel(context.Context, string, string, string) error
	DeleteChannel(context.Context, string) error
}

type ChannelAudioManager interface {
	ManageChannelAudio(context.Context, string, string, string, string, uint32) error
}

func (s *Service) ManageChannel(sessionID, action, id, name, description string) error {
	return s.ManageChannelContext(context.Background(), sessionID, action, id, name, description)
}

func (s *Service) ManageChannelContext(ctx context.Context, sessionID, action, id, name, description string, audioBitrate ...uint32) error {
	var bitrate uint32
	if len(audioBitrate) != 0 {
		bitrate = audioBitrate[0]
	}
	if bitrate != 0 && (bitrate < 16000 || bitrate > 64000 || bitrate%1000 != 0 || action == "delete") {
		return errors.New("频道目标码率须为 16–64 kbps 的整数")
	}
	name = strings.TrimSpace(name)
	if action != "delete" && (!utf8.ValidString(name) || name == "" || utf8.RuneCountInString(name) > 100 || strings.ContainsFunc(name, unicode.IsControl) || !utf8.ValidString(description) || len(description) > 1024 || strings.ContainsRune(description, 0)) {
		return errors.New("频道名称须为 1–100 个字符，描述最多 1024 字节")
	}
	s.mu.Lock()
	if s.shutdown || sessionID == "" || s.state.Session.ID != sessionID || s.state.Session.Mode != "connected" || !s.state.Session.CanManageChannels {
		s.mu.Unlock()
		return errors.New("当前会话没有频道管理权限")
	}
	remote, ok := s.connection.(ChannelManager)
	if !ok {
		s.mu.Unlock()
		return errors.New("当前协议不支持频道管理")
	}
	audioManager, supportsAudio := s.connection.(ChannelAudioManager)
	if bitrate != 0 && (!supportsAudio || !s.state.Session.CanConfigureChannelAudio) {
		s.mu.Unlock()
		return errors.New("服务器不支持频道音质设置")
	}
	if s.channelMutationSession == sessionID {
		s.mu.Unlock()
		return errors.New("频道操作正在进行")
	}
	if action != "create" {
		found := false
		for _, ch := range s.state.Channels {
			if ch.ID != id {
				continue
			}
			found = true
			if action == "delete" && (ch.IsDefault || ch.Members != 0) {
				s.mu.Unlock()
				return errors.New("默认频道或有成员的频道不能删除")
			}
		}
		if !found {
			s.mu.Unlock()
			return errors.New("频道已不存在")
		}
	}
	s.channelMutationSession = sessionID
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.channelMutationSession == sessionID {
			s.channelMutationSession = ""
		}
		s.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var err error
	if bitrate != 0 {
		err = audioManager.ManageChannelAudio(ctx, action, id, name, description, bitrate)
	} else {
		switch action {
		case "create":
			err = remote.CreateChannel(ctx, name, description)
		case "update":
			err = remote.UpdateChannel(ctx, id, name, description)
		case "delete":
			err = remote.DeleteChannel(ctx, id)
		default:
			return errors.New("未知频道操作")
		}
	}
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown || s.state.Session.ID != sessionID {
		return errors.New("会话已改变，请重新查看频道")
	}
	return nil
}
