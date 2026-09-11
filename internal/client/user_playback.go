package client

import (
	"errors"
	"strconv"

	"github.com/tsukiyoz/resona/internal/audio"
)

type peerPlaybackEngine interface {
	SetPeerPlayback(map[uint16]audio.PeerPlayback)
}

// SetUserPlayback changes local output only. The complete target is validated
// under the same lock as membership updates, before touching the audio engine.
func (s *Service) SetUserPlayback(sessionID, userID, instance string, volume int, muted bool) (Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown || s.state.Session.Mode != "connected" || s.state.Session.ID != sessionID || s.state.Session.SwitchingChannelID != "" {
		return Workspace{}, errors.New("会话已改变，请重新选择用户")
	}
	if volume < 0 || volume > 200 {
		return Workspace{}, errors.New("单人音量必须在0到200之间")
	}
	if _, ok := s.connection.(audio.Transport); !ok {
		return Workspace{}, errors.New("当前连接不支持单人音量")
	}
	for i := range s.state.Users {
		user := &s.state.Users[i]
		if user.ID != userID || user.Instance != instance || instance == "" {
			continue
		}
		if user.Self || user.ChannelID != s.state.Session.ChannelID {
			return Workspace{}, errors.New("只能调整当前频道其他成员的收听音量")
		}
		if _, err := strconv.ParseUint(userID, 10, 16); err != nil {
			return Workspace{}, errors.New("用户语音标识无效")
		}
		user.PlaybackVolume, user.PlaybackMuted = volume, muted
		s.syncUserPlaybackLocked()
		s.notifyChangedLocked()
		return s.snapshot(), nil
	}
	return Workspace{}, errors.New("该用户已离开或身份已改变")
}

func reconcileUserPlayback(previous, next []User) []User {
	byID := make(map[string]User, len(previous))
	for _, user := range previous {
		byID[user.ID] = user
	}
	users := cloneUsers(next)
	for i := range users {
		user := &users[i]
		user.PlaybackVolume, user.PlaybackMuted = 100, false
		if old, ok := byID[user.ID]; ok && user.Instance != "" && old.Instance == user.Instance {
			user.PlaybackVolume, user.PlaybackMuted = old.PlaybackVolume, old.PlaybackMuted
		}
	}
	return users
}

func (s *Service) syncUserPlaybackLocked() {
	engine, ok := s.voice.(peerPlaybackEngine)
	if !ok {
		return
	}
	peers := make(map[uint16]audio.PeerPlayback)
	for _, user := range s.state.Users {
		if user.Self || user.ChannelID != s.state.Session.ChannelID {
			continue
		}
		id, err := strconv.ParseUint(user.ID, 10, 16)
		if err == nil {
			peers[uint16(id)] = audio.PeerPlayback{Instance: user.Instance, Volume: user.PlaybackVolume, Muted: user.PlaybackMuted}
		}
	}
	engine.SetPeerPlayback(peers)
}
