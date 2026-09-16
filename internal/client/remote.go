package client

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
)

type RemoteConnector interface {
	// Connect blocks until the initial remote snapshot is ready. ctx only governs
	// connection setup; the returned connection owns the established session.
	Connect(context.Context, ServerProfile, string, func(RemoteState)) (RemoteConnection, error)
}

// ConnectFailure contains an adapter-authored, credential-free user message.
type ConnectFailure struct {
	Message   string
	Retryable bool
}

func (e *ConnectFailure) Error() string { return e.Message }

type RemoteConnection interface {
	Close() error
	// MoveChannel completes after the server acknowledges the move and publishes
	// the destination state. Cancellation must release the pending operation.
	MoveChannel(context.Context, string) error
}

var (
	ErrChannelPermissionDenied = errors.New("没有进入此频道的权限")
	ErrChannelPasswordRequired = errors.New("此频道需要密码，暂不支持进入")
)

type RemoteState struct {
	ServerName               string
	ChannelID                string
	SelfID                   string
	IdentityUID              string
	ServerRole               string
	CanClaimOwner            bool
	CanManageChannels        bool
	CanConfigureChannelAudio bool
	Channels                 []Channel
	Users                    []User
	MemberSyncState          string
	MemberSyncError          string
	// Messages contains only text received in this update, not a history snapshot.
	Messages []RemoteMessage
	// Events contains only changes from this update, never a cumulative history.
	Events []RemoteEvent
	// Error must be safe for direct display; adapters remove technical details
	// and credentials before publishing it.
	Error     string
	Closed    bool
	Retryable bool
}

func (s *Service) ConnectServer(id, password string) (Workspace, error) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.connectServerLocked(id, password, false, "")
}

func (s *Service) connectServerLocked(id, password string, remember bool, expectedAddress string) (Workspace, error) {
	s.mu.Lock()
	if s.shutdown {
		s.mu.Unlock()
		return Workspace{}, errors.New("客户端已经关闭")
	}
	if s.connector == nil {
		s.mu.Unlock()
		return Workspace{}, errors.New("当前版本未配置远程连接")
	}
	if s.remoteActiveLocked() || s.connectDone != nil {
		s.mu.Unlock()
		return Workspace{}, errors.New("已有服务器会话，请先断开")
	}
	if s.state.Session.Mode == "preview" {
		s.mu.Unlock()
		return Workspace{}, errors.New("请先退出本地预览，再连接服务器")
	}
	profile, ok := s.profileLocked(id)
	if !ok {
		s.mu.Unlock()
		return Workspace{}, errors.New("服务器书签不存在")
	}
	if expectedAddress != "" && ProfileDestination(profile) != expectedAddress {
		s.mu.Unlock()
		return Workspace{}, errors.New("服务器连接目标已更改，请重新连接")
	}
	if err := ValidateServerTrust(profile); err != nil {
		s.mu.Unlock()
		return Workspace{}, err
	}

	s.stopMicrophoneTestLocked()
	s.reconnect = nil
	s.generation++
	generation := s.generation
	ctx, cancel := context.WithTimeout(context.Background(), s.connectTimeout)
	done := make(chan struct{})
	s.connectCancel = cancel
	s.connectDone = done
	s.state.Session = Session{
		Protocol:   profile.Protocol,
		ID:         rand.Text(),
		Mode:       "connecting",
		Nickname:   profile.Nickname,
		ServerID:   profile.ID,
		ServerName: profile.Name,
	}
	s.clearRemoteCollectionsLocked()
	s.state.Notifications = []Notification{}
	s.notifyChangedLocked()
	state := s.snapshot()
	connector := s.connector
	slog.Info("connection requested")
	s.mu.Unlock()

	go func() {
		defer cancel()
		s.connect(ctx, connector, profile, password, generation, done, remember)
	}()
	return state, nil
}

func (s *Service) connect(ctx context.Context, connector RemoteConnector, profile ServerProfile, password string, generation uint64, done chan struct{}, remember bool) {
	defer func() {
		close(done)
		s.mu.Lock()
		if s.connectDone == done {
			s.connectCancel = nil
			s.connectDone = nil
		}
		s.mu.Unlock()
	}()
	connection, err := connector.Connect(ctx, profile, password, func(state RemoteState) {
		s.applyRemoteState(generation, state)
	})

	s.mu.Lock()
	if generation != s.generation || s.state.Session.Mode != "connecting" {
		s.mu.Unlock()
		if connection != nil {
			_ = connection.Close()
		}
		return
	}
	if err != nil || ctx.Err() != nil || connection == nil {
		slog.Warn("connection failed", "timeout", errors.Is(ctx.Err(), context.DeadlineExceeded))
		s.state.Session.Mode = "failed"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			s.state.Session.Error = "连接服务器超时，请稍后重试"
		} else {
			s.state.Session.Error = "连接服务器失败，请检查地址、密码和网络"
			var failure *ConnectFailure
			if errors.As(err, &failure) {
				s.state.Session.Error = failure.Message
			}
		}
		s.clearRemoteCollectionsLocked()
		s.notifyChangedLocked()
		s.mu.Unlock()
		if connection != nil {
			_ = connection.Close()
		}
		return
	}
	s.connection = connection
	s.reconnect = &reconnectPlan{profile: profile, password: password}
	slog.Info("connection established")
	s.state.Session.Mode = "connected"
	s.state.Session.Error = ""
	s.addNotificationLocked("connected", s.state.Session.ChannelID)
	s.notifyChangedLocked()
	s.mu.Unlock()
	_, _ = s.ConfigureDefaultVoice(generation)
	if remember {
		s.rememberSuccessfulPassword(profile, password, generation)
	}
}

func (s *Service) applyRemoteState(generation uint64, remote RemoteState) {
	s.mu.Lock()
	if generation != s.generation || (s.state.Session.Mode != "connecting" && s.state.Session.Mode != "connected" && s.state.Session.Mode != "reconnecting") {
		s.mu.Unlock()
		return
	}
	wasConnected := s.state.Session.Mode == "connected"
	previousChannel := s.state.Session.ChannelID
	var recovery *reconnectPlan
	if remote.Closed && wasConnected && remote.Retryable && s.reconnect != nil {
		copy := *s.reconnect
		copy.channel = previousChannel
		copy.voice = s.voiceState.VoiceConfig
		if s.onlineTestRestore != nil {
			copy.voice = *s.onlineTestRestore
		}
		copy.voice.LocalMonitor = false
		copy.voice.Muted = true
		recovery = &copy
	}
	s.state.Session.ServerName = remote.ServerName
	s.state.Session.ChannelID = remote.ChannelID
	s.state.Session.SelfID = remote.SelfID
	s.state.Session.IdentityUID = remote.IdentityUID
	s.state.Session.ServerRole = remote.ServerRole
	s.state.Session.CanClaimOwner = remote.CanClaimOwner
	s.state.Session.CanManageChannels = remote.CanManageChannels
	s.state.Session.CanConfigureChannelAudio = remote.CanConfigureChannelAudio
	s.state.Session.MemberSyncState = remote.MemberSyncState
	s.state.Session.MemberSyncError = remote.MemberSyncError
	if remote.Error != "" {
		s.state.Session.Error = remote.Error
	}
	s.state.Channels = cloneChannels(remote.Channels)
	s.state.Users = s.reconcileScopedUserPlayback(remote.Users, remote.MemberSyncState == "limited")
	s.syncUserPlaybackLocked()
	var connection RemoteConnection
	var voice voiceEngine
	if remote.Closed {
		s.reconnect = nil
		slog.Warn("connection lost", "retryable", remote.Retryable)
		voice = s.detachVoiceLocked()
		if wasConnected {
			s.addNotificationLocked("disconnected", previousChannel)
		}
		s.cancelMoveLocked()
		s.cancelMessageLocked()
		connection = s.connection
		s.connection = nil
		s.state.Session.Mode = "failed"
		s.state.Session.ChannelID = previousChannel
		s.state.Session.Error = remote.Error
		if s.state.Session.Error == "" {
			s.state.Session.Error = "服务器连接已关闭"
		}
		s.clearRemotePresenceLocked()
	} else {
		if wasConnected && previousChannel != remote.ChannelID {
			s.cancelMessageLocked()
			s.state.Messages = []Message{}
			s.voiceChannelChangedLocked()
		}
		s.appendRemoteMessagesLocked(remote.Messages)
		if wasConnected {
			s.applyRemoteEventsLocked(remote.Events)
		}
	}
	if connection != nil || voice != nil {
		s.cleanupWG.Add(1)
	}
	s.notifyChangedLocked()
	if recovery != nil {
		s.startReconnectLocked(*recovery, connection, voice != nil)
		connection, voice = nil, nil
	}
	s.mu.Unlock()
	if connection != nil || voice != nil {
		go func() {
			defer s.cleanupWG.Done()
			if voice != nil {
				s.closeRetiredVoices()
			}
			if connection != nil {
				_ = connection.Close()
			}
		}()
	}
}

func (s *Service) DisconnectServer() (Workspace, error) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	state, err := s.disconnectRemote()
	s.cleanupWG.Wait()
	return state, err
}

func (s *Service) disconnectRemote() (Workspace, error) {
	s.mu.Lock()
	s.reconnect = nil
	slog.Info("connection cancelled by user")
	if s.state.Session.Mode == "preview" {
		s.mu.Unlock()
		return Workspace{}, errors.New("本地预览没有远程连接")
	}
	if !s.remoteActiveLocked() && s.connectDone == nil && s.connection == nil {
		s.setOfflineLocked()
		state := s.snapshot()
		s.mu.Unlock()
		return state, nil
	}
	if s.state.Session.Mode == "connected" {
		s.addNotificationLocked("disconnected", s.state.Session.ChannelID)
	}
	s.generation++
	s.state.Session.Mode = "disconnecting"
	s.notifyChangedLocked()
	s.cancelMoveLocked()
	s.cancelMessageLocked()
	cancel := s.connectCancel
	done := s.connectDone
	connection := s.connection
	voice := s.detachVoiceLocked()
	s.connectCancel = nil
	s.connectDone = nil
	s.connection = nil
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	var closeErr error
	if voice != nil {
		s.closeRetiredVoices()
	}
	if connection != nil {
		closeErr = connection.Close()
	}

	s.mu.Lock()
	s.setOfflineLocked()
	state := s.snapshot()
	s.mu.Unlock()
	if closeErr != nil {
		return state, errors.New("断开服务器连接时发生错误")
	}
	return state, nil
}

func (s *Service) Shutdown() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.mu.Lock()
	s.shutdown = true
	s.stopMicrophoneTestLocked()
	preview := s.state.Session.Mode == "preview"
	s.mu.Unlock()
	if preview {
		s.mu.Lock()
		s.setOfflineLocked()
		s.mu.Unlock()
		s.cleanupWG.Wait()
		return
	}
	_, _ = s.disconnectRemote()
	s.cleanupWG.Wait()
}

func (s *Service) profileLocked(id string) (ServerProfile, bool) {
	for _, profile := range s.state.Servers {
		if profile.ID == id {
			return profile, true
		}
	}
	return ServerProfile{}, false
}

func (s *Service) remoteActiveLocked() bool {
	switch s.state.Session.Mode {
	case "connecting", "reconnecting", "connected", "disconnecting":
		return true
	default:
		return false
	}
}

func (s *Service) clearRemoteCollectionsLocked() {
	s.state.Channels = []Channel{}
	s.state.Users = []User{}
	s.state.Messages = []Message{}
}

func (s *Service) setOfflineLocked() {
	defer s.notifyChangedLocked()
	s.state.Messages = []Message{}
	if s.state.Session.ID != "" {
		s.state.Session.Mode = "offline"
		s.state.Session.Error = ""
		s.state.Session.MemberSyncState = ""
		s.state.Session.MemberSyncError = ""
		s.state.Session.CredentialError = ""
		s.clearRemotePresenceLocked()
		return
	}
	s.state.Session = Session{Mode: "offline", Nickname: "Resona"}
	s.clearRemoteCollectionsLocked()
}

func (s *Service) clearRemotePresenceLocked() {
	s.state.Users = []User{}
	for i := range s.state.Channels {
		s.state.Channels[i].Members = 0
	}
}

func cloneChannels(channels []Channel) []Channel {
	if channels == nil {
		return []Channel{}
	}
	return append([]Channel{}, channels...)
}

func cloneUsers(users []User) []User {
	if users == nil {
		return []User{}
	}
	return append([]User{}, users...)
}
