// Package ts3 adapts the pinned TS3 transport to Resona's client sessions.
package ts3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	teamspeak "github.com/honeybbq/teamspeak-go"
	"github.com/honeybbq/teamspeak-go/commands"
	"github.com/honeybbq/teamspeak-go/discovery"
	"github.com/tsukiyoz/resona/internal/audio"
	"github.com/tsukiyoz/resona/internal/client"
	"github.com/tsukiyoz/resona/internal/iconcache"
)

type Connector struct {
	identityPath string
	icons        *iconcache.Cache
}

// The application owns the optional cache. Without one, custom icons are disabled.
func New(identityPath string, stores ...*iconcache.Cache) *Connector {
	c := &Connector{identityPath: identityPath}
	if len(stores) > 0 {
		c.icons = stores[0]
	}
	return c
}

func NewDefault(stores ...*iconcache.Cache) (*Connector, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	return New(filepath.Join(dir, "resona", "identity.key"), stores...), nil
}

func (c *Connector) Connect(ctx context.Context, profile client.ServerProfile, password string, update func(client.RemoteState)) (client.RemoteConnection, error) {
	id, err := loadIdentity(ctx, c.identityPath)
	if err != nil {
		return nil, err
	}
	endpoint, err := resolveEndpoint(ctx, profile.Address)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lifetime, stop := context.WithCancel(context.Background())
	s := &connection{state: newReducer(identityUID(id)), update: update, ready: make(chan struct{}), ended: make(chan struct{}), changed: make(chan struct{}), commandGate: make(chan struct{}, 1), lifetime: lifetime, stop: stop}
	s.client = teamspeak.NewClient(id, endpoint, profile.Nickname,
		teamspeak.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		teamspeak.WithResolver(fixedResolver(endpoint)),
		teamspeak.WithServerPassword(password),
		teamspeak.WithCommandObserver(s.observe),
		teamspeak.WithVoiceObserver(s.observeVoice),
		teamspeak.WithInitialMute(true, true),
		teamspeak.WithCommandMiddleware(s.guardChannelText))
	s.execCommand = s.client.ExecCommandContext
	s.execCommandResponse = s.client.ExecCommandWithResponseContext
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		stop()
		return nil, errors.New("invalid resolved TS3 endpoint")
	}
	s.iconStore = c.icons
	if c.icons != nil {
		s.icons = newCachedIconLoader(lifetime, c.icons, func() iconcache.Key {
			s.mu.Lock()
			serverUID := s.state.serverUID
			s.mu.Unlock()
			return iconcache.Key{Protocol: "ts3", ServerUID: serverUID,
				Address: strings.ToLower(strings.TrimSpace(profile.Address)), Endpoint: endpoint,
				IdentityUID: identityUID(id)}
		}, func(ctx context.Context, iconID string) (string, error) {
			return downloadChannelIcon(ctx, host, iconID, s.client.FileTransferInitDownloadContext)
		}, s.publishIcon)
	}
	s.client.OnDisconnected(func(error) { s.disconnected() })
	// The endpoint is already numeric, so Connect does no blocking DNS work.
	if err := s.client.Connect(); err != nil {
		_ = s.Close()
		return nil, errors.New("cannot open TS3 connection")
	}
	select {
	case <-ctx.Done():
		_ = s.Close()
		return nil, ctx.Err()
	case <-s.ended:
		_ = s.Close()
		s.mu.Lock()
		err := errors.New(s.state.state.Error)
		s.mu.Unlock()
		return nil, err
	case <-s.ready:
		if err := ctx.Err(); err != nil {
			_ = s.Close()
			return nil, err
		}
		s.mu.Lock()
		closed, message := s.state.state.Closed, s.state.state.Error
		s.mu.Unlock()
		if closed {
			_ = s.Close()
			return nil, errors.New(message)
		}
		s.startMemberSubscription()
		return s, nil
	}
}

type connection struct {
	mu                   sync.Mutex
	client               *teamspeak.Client
	state                *reducer
	update               func(client.RemoteState)
	ready                chan struct{}
	ended                chan struct{}
	readyOnce            sync.Once
	endOnce              sync.Once
	closeOnce            sync.Once
	closing              bool
	closeErr             error
	changed              chan struct{}
	commandGate          chan struct{}
	lifetime             context.Context
	stop                 context.CancelFunc
	execCommand          func(context.Context, string) error
	execCommandResponse  func(context.Context, string) ([]map[string]string, error)
	subscribeStarted     bool
	background           sync.WaitGroup
	icons                *iconLoader
	iconStore            *iconcache.Cache
	voiceMu              sync.RWMutex
	voiceHandler         func(audio.Packet)
	voiceQueryRetryDelay time.Duration
	detailCache          map[string]detailCacheEntry
}

func (s *connection) observe(cmd teamspeak.IncomingCommand) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.state.state.Closed || !s.state.apply(cmd) {
		return
	}
	close(s.changed)
	s.changed = make(chan struct{})
	if s.state.state.Closed {
		s.stop()
		s.endOnce.Do(func() { close(s.ended) })
		s.update(s.state.snapshot())
		return
	}
	if s.state.ready() {
		if s.icons != nil {
			for _, channel := range s.state.channels {
				s.icons.Request(channel.IconID)
			}
		}
		s.update(s.state.snapshot())
		s.readyOnce.Do(func() { close(s.ready) })
	}
}

func (s *connection) disconnected() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.state.state.Closed {
		return
	}
	s.state.state.Closed = true
	s.state.state.Events = nil
	s.state.state.Messages = nil
	s.state.state.Error = "与服务器的连接已断开"
	s.stop()
	s.endOnce.Do(func() { close(s.ended) })
	s.update(s.state.snapshot())
}

func (s *connection) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		s.stop()
		s.mu.Unlock()
		s.closeErr = s.client.Disconnect()
		s.background.Wait()
		if s.icons != nil {
			s.icons.Close()
		}
	})
	return s.closeErr
}

func (s *connection) SetVoiceHandler(handler func(audio.Packet)) {
	s.voiceMu.Lock()
	s.voiceHandler = handler
	s.voiceMu.Unlock()
}

func (s *connection) observeVoice(packet teamspeak.VoicePacket) {
	if packet.Whisper {
		return
	}
	s.mu.Lock()
	instance := ""
	if s.state != nil {
		instance = s.state.users[strconv.FormatUint(uint64(packet.SenderID), 10)].Instance
	}
	if !packet.End && s.state != nil && !s.closing && !s.state.state.Closed {
		self, selfOK := s.state.users[s.state.state.SelfID]
		sender, senderOK := s.state.users[strconv.FormatUint(uint64(packet.SenderID), 10)]
		if selfOK && senderOK && sender.ChannelID == self.ChannelID {
			voice := s.state.voice[self.ChannelID]
			voice.unencrypted, voice.encryptionKnown = !packet.Encrypted, true
			s.state.voice[self.ChannelID] = voice
		}
	}
	s.mu.Unlock()
	s.voiceMu.RLock()
	defer s.voiceMu.RUnlock()
	if s.voiceHandler == nil {
		return
	}
	s.voiceHandler(audio.Packet{Instance: instance, ReceivedAt: packet.ReceivedAt, Sequence: packet.Sequence, SenderID: packet.SenderID, Codec: audio.Codec(packet.Codec), Data: packet.Data, End: packet.End})
}

func (s *connection) VoiceCodec() (audio.Codec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.state.state.Closed {
		return 0, context.Canceled
	}
	voice, err := s.state.currentVoiceChannel()
	if err != nil {
		return 0, err
	}
	codec := audio.Codec(voice.codec)
	if !codec.Supported() {
		return codec, fmt.Errorf("当前频道使用不支持的语音编码 %d", voice.codec)
	}
	return codec, nil
}

func (s *connection) SendVoice(data []byte, codec audio.Codec) error {
	s.mu.Lock()
	if s.closing || s.state.state.Closed {
		s.mu.Unlock()
		return context.Canceled
	}
	voice, err := s.state.currentVoiceChannel()
	unencrypted, encryptionErr := s.state.voiceEncryption(voice)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if encryptionErr != nil {
		ctx, cancel := context.WithTimeout(s.lifetime, 3*time.Second)
		voice, err = s.resolveVoiceChannel(ctx)
		cancel()
		if err != nil {
			return err
		}
		s.mu.Lock()
		unencrypted, err = s.state.voiceEncryption(voice)
		s.mu.Unlock()
		if err != nil {
			return err
		}
	}
	if audio.Codec(voice.codec) != codec {
		return errors.New("频道语音编码已改变")
	}
	if unencrypted {
		return s.client.SendVoice(data, byte(codec))
	}
	return s.client.SendVoiceEncrypted(data, byte(codec))
}

func (s *connection) resolveVoiceChannel(ctx context.Context) (voiceChannel, error) {
	s.mu.Lock()
	if s.closing || s.state.state.Closed {
		s.mu.Unlock()
		return voiceChannel{}, context.Canceled
	}
	voice, err := s.state.currentVoiceChannel()
	if err != nil {
		s.mu.Unlock()
		return voiceChannel{}, err
	}
	if _, encryptionErr := s.state.voiceEncryption(voice); encryptionErr == nil {
		s.mu.Unlock()
		return voice, nil
	}
	self := s.state.users[s.state.state.SelfID]
	s.mu.Unlock()

	if s.execCommandResponse == nil {
		return voiceChannel{}, errors.New("服务器未提供当前频道的语音加密状态")
	}
	select {
	case s.commandGate <- struct{}{}:
		defer func() { <-s.commandGate }()
	case <-ctx.Done():
		return voiceChannel{}, ctx.Err()
	}
	rows, queryErr := s.queryVoiceChannel(ctx, commands.BuildCommand("channelinfo", map[string]string{"cid": self.ChannelID}))
	if queryErr != nil {
		if ctx.Err() != nil {
			return voiceChannel{}, ctx.Err()
		}
		var commandError *teamspeak.CommandError
		if errors.As(queryErr, &commandError) {
			return voiceChannel{}, fmt.Errorf("无法读取当前频道的语音加密状态（错误码 %d）", commandError.ID)
		}
		return voiceChannel{}, errors.New("无法读取当前频道的语音加密状态")
	}
	if len(rows) == 0 {
		return voiceChannel{}, errors.New("服务器未提供当前频道的语音加密状态")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.state.state.Closed || s.state.users[s.state.state.SelfID].ChannelID != self.ChannelID {
		return voiceChannel{}, context.Canceled
	}
	current := s.state.voice[self.ChannelID]
	if value, ok := rows[0]["channel_codec"]; ok {
		parsed, parseErr := strconv.ParseUint(value, 10, 8)
		if parseErr == nil {
			current.codec, current.codecKnown = uint8(parsed), true
		}
	}
	if value, ok := rows[0]["channel_codec_is_unencrypted"]; ok && (value == "0" || value == "1") {
		current.unencrypted, current.encryptionKnown = value == "1", true
	}
	s.state.voice[self.ChannelID] = current
	if _, encryptionErr := s.state.voiceEncryption(current); encryptionErr != nil {
		return voiceChannel{}, encryptionErr
	}
	return current, nil
}

func (s *connection) queryVoiceChannel(ctx context.Context, raw string) ([]map[string]string, error) {
	rows, err := s.execCommandResponse(ctx, raw)
	var commandError *teamspeak.CommandError
	if !errors.As(err, &commandError) || commandError.ID != 524 {
		return rows, err
	}
	delay := s.voiceQueryRetryDelay
	if delay <= 0 {
		delay = time.Second
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.execCommandResponse(ctx, raw)
}

func (s *connection) SetVoiceMuted(ctx context.Context, inputMuted, outputMuted bool) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.lifetime, cancel)
	defer stop()
	select {
	case s.commandGate <- struct{}{}:
		defer func() { <-s.commandGate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	cmd := commands.BuildCommand("clientupdate", map[string]string{
		"client_input_muted": boolParam(inputMuted), "client_output_muted": boolParam(outputMuted),
	})
	if err := s.execCommand(ctx, cmd); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var commandError *teamspeak.CommandError
		if errors.As(err, &commandError) {
			return fmt.Errorf("服务器拒绝更新语音状态（错误码 %d）", commandError.ID)
		}
		return errors.New("无法更新服务器语音状态，请检查网络后重试")
	}
	return nil
}

func boolParam(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

// MoveChannel waits for both command acceptance and the observed own membership.
// It only moves this connection, never an arbitrary client ID supplied by the UI.
func (s *connection) MoveChannel(ctx context.Context, id string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.lifetime, cancel)
	defer stop()
	select {
	case s.commandGate <- struct{}{}:
		defer func() { <-s.commandGate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	channel, exists := s.state.channels[id]
	selfID := s.state.state.SelfID
	current := s.state.users[selfID].ChannelID
	closed := s.closing || s.state.state.Closed
	s.mu.Unlock()
	if closed {
		return context.Canceled
	}
	if !exists || !validID(id) || !validID(selfID) {
		return errors.New("频道不存在")
	}
	if channel.PasswordRequired {
		return client.ErrChannelPasswordRequired
	}
	if channel.Kind == "separator" {
		return errors.New("分隔项不能加入")
	}
	if current == id {
		return nil
	}
	cmd := commands.BuildCommand("clientmove", map[string]string{"clid": selfID, "cid": id})
	if err := s.execCommand(ctx, cmd); err != nil {
		var commandError *teamspeak.CommandError
		if errors.As(err, &commandError) {
			switch commandError.ID {
			case 0x0a08:
				return client.ErrChannelPermissionDenied
			case 0x030d:
				return client.ErrChannelPasswordRequired
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("服务器拒绝了频道切换")
	}
	for {
		s.mu.Lock()
		current, changed := s.state.users[selfID].ChannelID, s.changed
		closed := s.closing || s.state.state.Closed
		s.mu.Unlock()
		if closed {
			return context.Canceled
		}
		if current == id {
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type fixedResolver string

func (r fixedResolver) Resolve(context.Context, string) ([]discovery.ResolvedAddr, error) {
	return []discovery.ResolvedAddr{{Addr: string(r), Source: "Resona"}}, nil
}

// M1 supports host/IP with optional port and TS3 SRV discovery. Resolve before
// opening UDP so a canceled GUI connection cannot be stuck in library DNS.
func resolveEndpoint(ctx context.Context, address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	explicitPort := err == nil
	if !explicitPort {
		host, port = strings.Trim(address, "[]"), "9987"
	}
	if net.ParseIP(host) == nil && !explicitPort {
		_, records, err := net.DefaultResolver.LookupSRV(ctx, "ts3", "udp", host)
		if err == nil && len(records) > 0 {
			host = strings.TrimSuffix(records[0].Target, ".")
			port = strconv.Itoa(int(records[0].Port))
		}
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New("cannot resolve TS3 server address")
	}
	return net.JoinHostPort(ips[0].String(), port), nil
}
