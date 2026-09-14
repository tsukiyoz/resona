// Package native adapts the Resona QUIC protocol to the existing client/audio contracts.
package native

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/tsukiyoz/resona/internal/audio"
	"github.com/tsukiyoz/resona/internal/client"
	"github.com/tsukiyoz/resona/internal/nativeidentity"
	w "github.com/tsukiyoz/resona/internal/nativewire"
	"github.com/tsukiyoz/resona/internal/noiseudp"
)

// An empty IdentityPath explicitly uses an ephemeral identity for tests/bots.
// Product construction uses NewDefault and a native-only persistent seed file.
type Connector struct{ IdentityPath string }

func NewDefault() (Connector, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return Connector{}, err
	}
	return Connector{IdentityPath: filepath.Join(dir, "resona", "native-identity.key")}, nil
}

type connection struct {
	voiceBitrate atomic.Uint32
	conn         w.Connection
	stream       w.Stream
	mu           sync.Mutex
	state        w.State
	pending      map[uint32]chan uint8
	next         uint32
	gate         chan struct{}
	sequence     uint16
	onState      func(client.RemoteState)
	voiceMu      sync.RWMutex
	voiceHandler func(audio.Packet)
	voiceOut     chan w.QueuedVoice
	workers      sync.WaitGroup
}

func (connector Connector) Connect(ctx context.Context, profile client.ServerProfile, password string, update func(client.RemoteState)) (client.RemoteConnection, error) {
	var identity ed25519.PrivateKey
	var err error
	if connector.IdentityPath == "" {
		_, identity, err = ed25519.GenerateKey(rand.Reader)
	} else {
		identity, err = nativeidentity.LoadOrCreate(connector.IdentityPath)
	}
	if err != nil {
		return nil, &client.ConnectFailure{Message: "无法加载原生身份，原文件已保留"}
	}
	tlsConfig, err := w.ClientTLS(profile.CertificateFingerprint)
	if err != nil {
		return nil, err
	}
	address := profile.Address
	if net.ParseIP(address) != nil {
		address = net.JoinHostPort(address, w.DefaultPort)
	} else if _, _, err := net.SplitHostPort(address); err != nil {
		address = net.JoinHostPort(address, w.DefaultPort)
	}
	var q w.Connection
	if profile.Protocol == "resona-noise" {
		key, keyErr := hex.DecodeString(profile.ServerPublicKey)
		if keyErr != nil || len(key) != 32 {
			return nil, &client.ConnectFailure{Message: "Noise 服务器公钥无效"}
		}
		q, err = w.DialNoise(ctx, address, key)
	} else {
		q, err = w.DialQUIC(ctx, address, tlsConfig)
	}
	if err != nil {
		if profile.Protocol == "resona-noise" {
			return nil, &client.ConnectFailure{Message: "Noise 连接失败，请核对服务器公钥、地址及 UDP 网络"}
		}
		return nil, &client.ConnectFailure{Message: "原生 QUIC 连接失败，请检查地址、证书信任、协议版本和 UDP 网络"}
	}
	success := false
	defer func() {
		if !success {
			_ = q.CloseWithError(0, "setup cancelled")
		}
	}()
	stream, err := q.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = q.CloseWithError(0, "setup cancelled") })
	defer stop()
	_ = stream.SetDeadline(time.Now().Add(8 * time.Second))
	hello := w.Hello{Nickname: profile.Nickname, Password: password}
	if err = w.SignHello(q, identity, &hello); err != nil {
		return nil, err
	}
	if err = w.Write(stream, w.HelloKind, 0, hello); err != nil {
		return nil, err
	}
	frame, err := w.Read(stream)
	if err != nil {
		var applicationError *quic.ApplicationError
		var noiseError *noiseudp.RemoteError
		if (errors.As(err, &applicationError) && applicationError.ErrorCode == w.AuthenticationFailed) || (errors.As(err, &noiseError) && noiseError.Code == w.AuthenticationFailed) {
			return nil, &client.ConnectFailure{Message: "原生服务器拒绝连接，请检查服务器密码"}
		}
		return nil, err
	}
	if frame.Kind == w.ReplyKind {
		return nil, &client.ConnectFailure{Message: "原生服务器拒绝连接，请检查服务器密码"}
	}
	var state w.State
	if frame.Kind != w.WelcomeKind || frame.Request != 0 || w.Decode(frame, &state) != nil || !validState(state) || !q.SupportsDatagrams() {
		return nil, errors.New("invalid native welcome")
	}
	if state.IdentityUID != nativeidentity.UID(hello.PublicKey) {
		return nil, errors.New("server returned wrong identity")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	_ = stream.SetDeadline(time.Time{})
	c := &connection{conn: q, stream: stream, state: state, pending: map[uint32]chan uint8{}, gate: make(chan struct{}, 1), onState: update, voiceOut: make(chan w.QueuedVoice, 4)}
	c.voiceBitrate.Store(stateVoiceBitrate(state))
	update(c.remote(state, nil))
	c.workers.Add(3)
	go func() { defer c.workers.Done(); c.readLoop() }()
	go func() { defer c.workers.Done(); c.voiceLoop() }()
	go func() { defer c.workers.Done(); w.SendVoiceQueue(q, c.voiceOut) }()
	success = true
	return c, nil
}
func validState(s w.State) bool {
	if s.Self == 0 || s.Epoch == 0 || len(s.Channels) == 0 || len(s.Channels) > w.MaxChannels || len(s.Members) > w.MaxMembers {
		return false
	}
	channels := map[uint16]bool{}
	for _, ch := range s.Channels {
		if ch.ID == 0 || channels[ch.ID] || !w.ValidChannelBitrate(ch.Bitrate) {
			return false
		}
		channels[ch.ID] = true
	}
	users := map[uint16]bool{}
	for _, u := range s.Members {
		if u.ID == 0 || u.Epoch == 0 || users[u.ID] || !channels[u.Channel] || u.Instance == "" {
			return false
		}
		users[u.ID] = true
	}
	return users[s.Self]
}
func id(v uint16) string { return strconv.Itoa(int(v)) }
func (c *connection) remote(s w.State, messages []client.RemoteMessage) client.RemoteState {
	r := client.RemoteState{ServerName: s.Name, SelfID: id(s.Self), MemberSyncState: "ready", Messages: messages, IdentityUID: s.IdentityUID, ServerRole: s.ServerRole, CanClaimOwner: s.CanClaimOwner}
	r.CanManageChannels = s.CanManageChannels
	r.CanConfigureChannelAudio = s.CanConfigureChannelAudio
	for i, ch := range s.Channels {
		r.Channels = append(r.Channels, client.Channel{ID: id(ch.ID), Name: ch.Name, Description: ch.Description, Kind: "channel", Order: strconv.Itoa(i), IsDefault: i == 0, Bitrate: w.ChannelBitrate(ch.Bitrate)})
	}
	for _, u := range s.Members {
		r.Users = append(r.Users, client.User{ID: id(u.ID), Nickname: u.Nickname, ChannelID: id(u.Channel), Self: u.ID == s.Self, Instance: u.Instance, PlaybackVolume: 100})
		if u.ID == s.Self {
			r.ChannelID = id(u.Channel)
		}
		for i := range r.Channels {
			if r.Channels[i].ID == id(u.Channel) {
				r.Channels[i].Members++
			}
		}
	}
	return r
}
func (c *connection) readLoop() {
	defer func() {
		_ = c.conn.CloseWithError(0, "control ended")
		c.mu.Lock()
		state := c.state
		c.mu.Unlock()
		r := c.remote(state, nil)
		r.Closed = true
		r.Error = "原生服务器连接已关闭"
		c.onState(r)
	}()
	for {
		f, err := w.Read(c.stream)
		if err != nil {
			return
		}
		switch f.Kind {
		case w.StateKind:
			var s w.State
			if w.Decode(f, &s) != nil || !validState(s) {
				return
			}
			c.mu.Lock()
			if s.Self != c.state.Self || s.Epoch < c.state.Epoch || s.IdentityUID != c.state.IdentityUID {
				c.mu.Unlock()
				return
			}
			c.state = s
			c.voiceBitrate.Store(stateVoiceBitrate(s))
			c.mu.Unlock()
			c.onState(c.remote(s, nil))
		case w.ReplyKind:
			var r w.Reply
			if w.Decode(f, &r) != nil {
				return
			}
			c.mu.Lock()
			ch := c.pending[f.Request]
			c.mu.Unlock()
			if ch != nil {
				select {
				case ch <- r.Code:
				default:
				}
			}
		case w.MessageKind:
			var m w.Message
			if w.Decode(f, &m) != nil || len(m.Text) > client.MaxChannelMessageBytes {
				return
			}
			c.mu.Lock()
			s := c.state
			c.mu.Unlock()
			c.onState(c.remote(s, []client.RemoteMessage{{ChannelID: id(m.Channel), UserID: id(m.Sender), Author: m.Nickname, Text: m.Text}}))
		default:
			return
		}
	}
}
func (c *connection) ClaimOwner(ctx context.Context, token string) error {
	if len(token) != 64 {
		return errors.New("认领码格式无效")
	}
	if err := c.command(ctx, w.ClaimOwnerKind, w.ClaimOwner{Token: token}); err != nil {
		return errors.New("认领未成功确认，请检查认领码及服务器角色状态")
	}
	return nil
}

func (c *connection) command(ctx context.Context, kind uint8, cmd any) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	select {
	case c.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.conn.Context().Done():
		return context.Canceled
	}
	defer func() { <-c.gate }()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	c.mu.Lock()
	if c.next == ^uint32(0) {
		c.mu.Unlock()
		return errors.New("request sequence exhausted")
	}
	c.next++
	request := c.next
	reply := make(chan uint8, 1)
	c.pending[request] = reply
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, request); c.mu.Unlock() }()
	deadline := time.Now().Add(8 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.stream.SetWriteDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = c.conn.CloseWithError(0, "operation cancelled") })
	defer stop()
	if err := w.Write(c.stream, kind, request, cmd); err != nil {
		_ = c.conn.CloseWithError(1, "control write failed")
		return err
	}
	select {
	case code := <-reply:
		if ctx.Err() != nil {
			_ = c.conn.CloseWithError(0, "operation cancelled")
			return ctx.Err()
		}
		if kind >= w.CreateChannelKind && kind <= w.DeleteChannelKind {
			return channelReply(code)
		}
		switch code {
		case w.OK:
			return nil
		case w.WrongChannel:
			return client.ErrMessageChannelChanged
		case w.RateLimited:
			return client.ErrMessageRateLimited
		case w.PermissionDenied:
			return errors.New("没有频道管理权限")
		case w.ChannelNotEmpty:
			return errors.New("频道内仍有成员，不能删除")
		case w.DefaultChannel:
			return errors.New("默认频道不能删除")
		case w.StorageFailed:
			return errors.New("服务器保存失败，结果未确认，请检查频道列表后再操作")
		case w.ChannelLimit:
			return errors.New("频道数量或 ID 已达上限")
		default:
			return &client.MessageSendError{Message: "服务器拒绝此操作"}
		}
	case <-ctx.Done():
		_ = c.conn.CloseWithError(0, "operation cancelled")
		return ctx.Err()
	case <-c.conn.Context().Done():
		return context.Canceled
	}
}
func (c *connection) MoveChannel(ctx context.Context, channel string) error {
	n, err := strconv.ParseUint(channel, 10, 16)
	if err != nil {
		return err
	}
	return c.command(ctx, w.MoveKind, w.Command{Channel: uint16(n)})
}
func (c *connection) SendChannelMessage(ctx context.Context, channel, text string) error {
	n, err := strconv.ParseUint(channel, 10, 16)
	if err != nil {
		return client.ErrMessageChannelChanged
	}
	return c.command(ctx, w.ChatKind, w.Command{Channel: uint16(n), Text: text})
}
func (c *connection) Close() error {
	_ = c.conn.CloseWithError(0, "client disconnect")
	c.workers.Wait()
	c.SetVoiceHandler(nil)
	return nil
}
func (c *connection) SetVoiceHandler(fn func(audio.Packet)) {
	c.voiceMu.Lock()
	c.voiceHandler = fn
	c.voiceMu.Unlock()
}
func (c *connection) VoiceCodec() (audio.Codec, error) {
	if c.conn.Context().Err() != nil {
		return 0, context.Canceled
	}
	return audio.CodecOpusVoice, nil
}

// Read by the encoder at frame boundaries; control updates never touch Opus.
func (c *connection) VoiceBitrate() int { return int(w.ChannelBitrate(c.voiceBitrate.Load())) }

func stateVoiceBitrate(s w.State) uint32 {
	for _, member := range s.Members {
		if member.ID != s.Self {
			continue
		}
		for _, ch := range s.Channels {
			if ch.ID == member.Channel {
				return w.ChannelBitrate(ch.Bitrate)
			}
		}
	}
	return 48000
}
func (c *connection) SetVoiceMuted(ctx context.Context, muted, deafened bool) error {
	return c.command(ctx, w.VoiceStateKind, w.Command{Muted: muted, Deafened: deafened})
}
func (c *connection) SendVoice(data []byte, codec audio.Codec) error {
	if codec != audio.CodecOpusVoice {
		return audio.ErrUnsupportedCodec
	}
	if c.conn.Context().Err() != nil {
		return context.Canceled
	}
	c.mu.Lock()
	c.sequence++
	v := w.Voice{Epoch: c.state.Epoch, Sequence: c.sequence, End: len(data) == 0, Data: data}
	c.mu.Unlock()
	b, err := w.EncodeVoice(v, false)
	if err != nil {
		return err
	}
	select {
	case c.voiceOut <- w.QueuedVoice{Data: b, At: time.Now()}:
	default:
	}
	return nil
}
func (c *connection) voiceLoop() {
	for {
		b, err := c.conn.ReceiveDatagram(c.conn.Context())
		if err != nil {
			return
		}
		v, err := w.DecodeVoice(b, true)
		if err != nil {
			continue
		}
		c.mu.Lock()
		s := c.state
		instance := ""
		ownChannel := uint16(0)
		for _, u := range s.Members {
			if u.ID == s.Self {
				ownChannel = u.Channel
			}
		}
		if v.Epoch == s.Epoch && v.Sender != s.Self {
			for _, u := range s.Members {
				if u.ID == v.Sender && u.Channel == ownChannel && u.Epoch == v.SenderEpoch {
					instance = u.Instance
				}
			}
		}
		c.mu.Unlock()
		if instance == "" {
			continue
		}
		c.voiceMu.RLock()
		if c.voiceHandler != nil {
			c.voiceHandler(audio.Packet{Instance: instance, ReceivedAt: time.Now(), Data: v.Data, Sequence: v.Sequence, SenderID: v.Sender, Codec: audio.CodecOpusVoice, End: v.End})
		}
		c.voiceMu.RUnlock()
	}
}
func (c *connection) ReadChannelDetails(ctx context.Context, channel string) (client.ChannelDetails, error) {
	if ctx.Err() != nil {
		return client.ChannelDetails{}, ctx.Err()
	}
	c.mu.Lock()
	s := c.state
	c.mu.Unlock()
	r := c.remote(s, nil)
	for _, ch := range r.Channels {
		if ch.ID == channel {
			codec := "Opus mono / 48 kHz / 20 ms / " + strconv.Itoa(int(ch.Bitrate)/1000) + " kbps"
			return client.ChannelDetails{ID: ch.ID, Name: ch.Name, Description: &ch.Description, Codec: &codec, Members: ch.Members, MemberSyncState: "ready"}, nil
		}
	}
	return client.ChannelDetails{}, errors.New("频道不存在")
}
func (c *connection) ReadUserDetails(ctx context.Context, user string) (client.UserDetails, error) {
	if ctx.Err() != nil {
		return client.UserDetails{}, ctx.Err()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, u := range c.state.Members {
		if id(u.ID) == user {
			return client.UserDetails{ID: user, Nickname: u.Nickname, ChannelID: id(u.Channel), InputMuted: &u.Muted, OutputMuted: &u.Deafened}, nil
		}
	}
	return client.UserDetails{}, errors.New("成员不存在")
}
