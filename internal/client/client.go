// Package client owns UI-independent workspace and local preview behavior.
package client

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tsukiyoz/resona/internal/audio"
)

type ServerProfile struct {
	Protocol            string `json:"protocol,omitempty"`
	ServerPublicKey     string `json:"serverPublicKey,omitempty"`
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Address             string `json:"address"`
	Nickname            string `json:"nickname"`
	SkipPasswordStorage bool   `json:"skipPasswordStorage,omitempty"`
}

type Session struct {
	Protocol                 string `json:"protocol,omitempty"`
	ID                       string `json:"id"`
	SendingMessageID         string `json:"sendingMessageID"`
	Mode                     string `json:"mode"`
	ChannelID                string `json:"channelID"`
	Nickname                 string `json:"nickname"`
	ServerID                 string `json:"serverID"`
	ServerName               string `json:"serverName"`
	IdentityUID              string `json:"identityUID"`
	ServerRole               string `json:"serverRole"`
	CanClaimOwner            bool   `json:"canClaimOwner"`
	CanManageChannels        bool   `json:"canManageChannels"`
	CanConfigureChannelAudio bool   `json:"canConfigureChannelAudio,omitempty"`
	SelfID                   string `json:"selfID"`
	Error                    string `json:"error"`
	SwitchingChannelID       string `json:"switchingChannelID"`
	CredentialError          string `json:"credentialError"`
	MemberSyncState          string `json:"memberSyncState"`
	MemberSyncError          string `json:"memberSyncError"`
}

type Channel struct {
	Bitrate          uint32 `json:"bitrate,omitempty"`
	IsDefault        bool   `json:"isDefault"`
	Kind             string `json:"kind"`
	Align            string `json:"align"`
	Repeat           bool   `json:"repeat"`
	IconID           string `json:"iconID"`
	IconRef          string `json:"iconRef"`
	ID               string `json:"id"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	Members          int    `json:"members"`
	ParentID         string `json:"parentID"`
	Order            string `json:"order"`
	PasswordRequired bool   `json:"passwordRequired"`
}

type User struct {
	VoiceStateKnown bool   `json:"voiceStateKnown,omitempty"`
	InputMuted      bool   `json:"inputMuted,omitempty"`
	OutputMuted     bool   `json:"outputMuted,omitempty"`
	Instance        string `json:"instance"`
	PlaybackVolume  int    `json:"playbackVolume"`
	PlaybackMuted   bool   `json:"playbackMuted"`
	ID              string `json:"id"`
	Nickname        string `json:"nickname"`
	ChannelID       string `json:"channelID"`
	Self            bool   `json:"self"`
}

type Message struct {
	AuthorID  string `json:"authorID"`
	Status    string `json:"status"`
	Error     string `json:"error"`
	ID        string `json:"id"`
	ChannelID string `json:"channelID"`
	Author    string `json:"author"`
	Text      string `json:"text"`
	CreatedAt string `json:"createdAt"`
}

type Workspace struct {
	Servers       []ServerProfile `json:"servers"`
	Session       Session         `json:"session"`
	Channels      []Channel       `json:"channels"`
	Users         []User          `json:"users"`
	Messages      []Message       `json:"messages"`
	Notifications []Notification  `json:"notifications"`
}

type ProfileStore interface {
	Load() ([]ServerProfile, error)
	Save([]ServerProfile) error
}

type Service struct {
	playbackCacheSession   string
	playbackCache          map[[2]string]userPlaybackPreference
	channelMutationSession string
	mu                     sync.Mutex
	lifecycleMu            sync.Mutex
	credentialMu           sync.Mutex
	passwords              PasswordStore
	store                  ProfileStore
	connector              RemoteConnector
	state                  Workspace
	subscribers            map[chan struct{}]struct{}
	activeReads            int
	voiceMu                sync.Mutex
	voice                  voiceEngine
	voiceState             VoiceState
	voiceEpoch             uint64
	voiceOperation         uint64
	retiredVoices          []voiceEngine
	voiceCancel            context.CancelFunc
	newVoice               func(audio.Transport, func(audio.VoiceState)) voiceEngine
	microphoneTestMu       sync.Mutex
	microphoneTest         voiceEngine
	onlineTestRestore      *audio.VoiceConfig
	onlineTestStopping     bool
	microphoneTestState    VoiceState
	microphoneTestCancel   context.CancelFunc
	retiredMicrophoneTests []voiceEngine
	newMicrophoneTest      func(func(audio.VoiceState)) voiceEngine

	connection      RemoteConnection
	connectCancel   context.CancelFunc
	connectDone     chan struct{}
	cleanupWG       sync.WaitGroup
	generation      uint64
	connectTimeout  time.Duration
	moveTimeout     time.Duration
	moveCancel      context.CancelFunc
	moveSequence    uint64
	messageTimeout  time.Duration
	messageCancel   context.CancelFunc
	messageSequence uint64
	shutdown        bool
}

func New(store ProfileStore) (*Service, error) {
	return NewWithConnector(store, nil)
}

func NewWithConnector(store ProfileStore, connector RemoteConnector) (*Service, error) {
	return NewWithPasswordStore(store, connector, nil)
}

func NewWithPasswordStore(store ProfileStore, connector RemoteConnector, passwords PasswordStore) (*Service, error) {
	profiles, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("load server profiles: %w", err)
	}
	if profiles == nil {
		profiles = []ServerProfile{}
	}
	return &Service{store: store, connector: connector, passwords: passwords, voiceState: VoiceState{VoiceConfig: defaultVoiceConfig()}, connectTimeout: 30 * time.Second, moveTimeout: 8 * time.Second, messageTimeout: 8 * time.Second, state: Workspace{
		Servers: profiles, Session: Session{Mode: "offline", Nickname: "Resona"},
		Channels: []Channel{}, Users: []User{}, Messages: []Message{}, Notifications: []Notification{},
	}}, nil
}

func (s *Service) GetWorkspace() (Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot(), nil
}

func (s *Service) SaveServer(profile ServerProfile) (Workspace, error) {
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.notifyChangedLocked()
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Address = strings.TrimSpace(profile.Address)
	profile.Nickname = strings.TrimSpace(profile.Nickname)
	profile.Protocol = strings.ToLower(strings.TrimSpace(profile.Protocol))
	profile.ServerPublicKey = strings.ToLower(strings.TrimSpace(profile.ServerPublicKey))
	if err := ValidateProfile(profile); err != nil {
		return Workspace{}, err
	}
	profiles := append([]ServerProfile{}, s.state.Servers...)
	if profile.ID == "" {
		profile.ID = rand.Text()
		profiles = append(profiles, profile)
	} else {
		if s.remoteActiveLocked() && s.state.Session.ServerID == profile.ID {
			return Workspace{}, errors.New("请先断开当前服务器，再编辑该书签")
		}
		found := false
		for i := range profiles {
			if profiles[i].ID == profile.ID {
				profile.SkipPasswordStorage = profiles[i].SkipPasswordStorage
				if ProfileDestination(profiles[i]) != ProfileDestination(profile) && s.passwords != nil {
					if err := s.passwords.Delete(passwordKey(profiles[i])); err != nil {
						return Workspace{}, errors.New("无法清除原服务器密码，连接目标未更改")
					}
				}
				profiles[i] = profile
				found = true
				break
			}
		}
		if !found {
			return Workspace{}, errors.New("server profile does not exist")
		}
	}
	if err := s.store.Save(profiles); err != nil {
		return Workspace{}, fmt.Errorf("save server profiles: %w", err)
	}
	s.state.Servers = profiles
	return s.snapshot(), nil
}

func (s *Service) DeleteServer(id string) (Workspace, error) {
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.notifyChangedLocked()
	if s.remoteActiveLocked() && s.state.Session.ServerID == id {
		return Workspace{}, errors.New("请先断开当前服务器，再删除该书签")
	}
	profiles := make([]ServerProfile, 0, len(s.state.Servers))
	found := false
	for _, profile := range s.state.Servers {
		if profile.ID == id {
			if s.passwords != nil {
				if err := s.passwords.Delete(passwordKey(profile)); err != nil {
					return Workspace{}, errors.New("无法清除服务器密码，书签未删除")
				}
			}
			found = true
			continue
		}
		profiles = append(profiles, profile)
	}
	if !found {
		return Workspace{}, errors.New("server profile does not exist")
	}
	if err := s.store.Save(profiles); err != nil {
		return Workspace{}, fmt.Errorf("save server profiles: %w", err)
	}
	s.state.Servers = profiles
	return s.snapshot(), nil
}

func (s *Service) OpenPreview() (Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.notifyChangedLocked()
	if s.remoteActiveLocked() {
		return Workspace{}, errors.New("请先断开当前服务器，再打开本地预览")
	}
	if s.state.Session.Mode == "preview" {
		return s.snapshot(), nil
	}
	s.state.Session = Session{Mode: "preview", ChannelID: "lobby", Nickname: "Resona"}
	s.state.Notifications = []Notification{}
	s.state.Channels = []Channel{
		{ID: "lobby", Name: "大厅", Description: "本地预览", Members: 1},
		{ID: "music", Name: "音乐", Description: "本地预览", Members: 0},
		{ID: "workshop", Name: "工作间", Description: "本地预览", Members: 0},
	}
	s.state.Messages = []Message{}
	s.state.Users = []User{{ID: "preview-self", Nickname: "Resona", ChannelID: "lobby", Self: true}}
	return s.snapshot(), nil
}

func (s *Service) LeavePreview() (Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.notifyChangedLocked()
	if s.remoteActiveLocked() {
		return Workspace{}, errors.New("当前是远程会话，不能退出本地预览")
	}
	s.state.Session = Session{Mode: "offline", Nickname: "Resona"}
	s.state.Channels = []Channel{}
	s.state.Users = []User{}
	s.state.Messages = []Message{}
	return s.snapshot(), nil
}

func (s *Service) SelectChannel(id string) (Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.notifyChangedLocked()
	if s.state.Session.Mode == "connected" {
		return s.selectRemoteChannelLocked(id)
	}
	if (s.state.Session.Mode == "offline" || s.state.Session.Mode == "failed") && s.state.Session.ID != "" {
		for _, channel := range s.state.Channels {
			if channel.ID == id && channel.Kind != "separator" {
				s.state.Session.ChannelID = id
				return s.snapshot(), nil
			}
		}
		return Workspace{}, errors.New("频道不存在")
	}
	if s.state.Session.Mode != "preview" {
		if s.remoteActiveLocked() {
			return Workspace{}, errors.New("请等待服务器连接完成，再选择频道")
		}
		return Workspace{}, errors.New("请先打开本地预览，再选择频道")
	}
	found := false
	for _, channel := range s.state.Channels {
		if channel.ID == id {
			found = true
			break
		}
	}
	if !found {
		return Workspace{}, errors.New("频道不存在")
	}
	for i := range s.state.Channels {
		s.state.Channels[i].Members = 0
		if s.state.Channels[i].ID == id {
			s.state.Channels[i].Members = 1
		}
	}
	s.state.Session.ChannelID = id
	for i := range s.state.Users {
		if s.state.Users[i].Self {
			s.state.Users[i].ChannelID = id
		}
	}
	return s.snapshot(), nil
}

func (s *Service) SendMessage(text string) (Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.notifyChangedLocked()
	if s.state.Session.Mode != "preview" {
		if s.remoteActiveLocked() {
			return Workspace{}, errors.New("真实服务器暂不支持发送消息")
		}
		return Workspace{}, errors.New("请先打开本地预览，再发送消息")
	}
	text = strings.TrimSpace(text)
	if !utf8.ValidString(text) || text == "" || utf8.RuneCountInString(text) > 2000 {
		return Workspace{}, errors.New("message must contain between 1 and 2000 characters")
	}
	s.state.Messages = append(s.state.Messages, Message{
		ID: rand.Text(), ChannelID: s.state.Session.ChannelID,
		Author: s.state.Session.Nickname, Text: text, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		AuthorID: "preview-self", Status: "sent",
	})
	// Keep the local preview bounded while retaining its most recent messages.
	if len(s.state.Messages) > 500 {
		s.state.Messages = append([]Message{}, s.state.Messages[len(s.state.Messages)-500:]...)
	}
	return s.snapshot(), nil
}

func (s *Service) snapshot() Workspace {
	return Workspace{
		Servers: append([]ServerProfile{}, s.state.Servers...), Session: s.state.Session,
		Channels: append([]Channel{}, s.state.Channels...), Users: append([]User{}, s.state.Users...),
		Messages:      append([]Message{}, s.state.Messages...),
		Notifications: append([]Notification{}, s.state.Notifications...),
	}
}

func ValidateProfile(profile ServerProfile) error {
	if err := ValidateServerTrust(profile); err != nil {
		return err
	}
	for _, field := range []struct {
		name  string
		value string
		limit int
	}{{"name", profile.Name, 100}, {"nickname", profile.Nickname, 30}} {
		if !utf8.ValidString(field.value) || strings.TrimSpace(field.value) == "" || utf8.RuneCountInString(field.value) > field.limit || strings.ContainsFunc(field.value, unicode.IsControl) {
			return fmt.Errorf("%s must contain between 1 and %d characters without control characters", field.name, field.limit)
		}
	}
	address := profile.Address
	if address == "" || len(address) > 253 || strings.ContainsAny(address, "/\\@?#") || strings.ContainsFunc(address, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return errors.New("address must be a hostname or IP address with an optional port")
	}
	if net.ParseIP(address) != nil {
		return nil
	}
	host := address
	if strings.Contains(address, ":") {
		var port string
		var err error
		host, port, err = net.SplitHostPort(address)
		if err != nil {
			return errors.New("invalid address; use hostname:port or [IPv6]:port")
		}
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 {
			return errors.New("port must be between 1 and 65535")
		}
	}
	if net.ParseIP(host) != nil {
		return nil
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return errors.New("invalid hostname")
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return errors.New("hostname must use ASCII letters, digits, hyphens and dots")
			}
		}
	}
	return nil
}

// The persisted marker rejects obsolete bookmarks before credentials or I/O.
// It is not a user-selectable transport.
func ValidateServerTrust(profile ServerProfile) error {
	if profile.Protocol != "resona-noise" {
		return errors.New("此书签版本已停止支持，请删除后使用服务器地址和公钥重新添加")
	}
	if len(profile.ServerPublicKey) != 64 {
		return errors.New("服务器公钥必须为64位X25519十六进制")
	}
	for _, r := range profile.ServerPublicKey {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return errors.New("服务器公钥包含无效字符")
		}
	}
	return nil
}
