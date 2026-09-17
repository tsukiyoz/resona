// Package server is the authoritative Resona native channel and relay service.
package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tsukiyoz/resona/internal/nativeidentity"
	w "github.com/tsukiyoz/resona/internal/nativewire"
)

type Config struct {
	ChannelStore *ChannelStore
	Ownership    *Ownership
	NoiseKey     []byte
	Name         string
	Password     string
	Channels     []w.Channel
	MaxClients   int
}
type Server struct {
	channelMu       sync.Mutex
	deletingChannel uint16
	listener        w.Listener
	config          Config
	mu              sync.Mutex
	peers           map[uint16]*peer
	next            uint32
	wg              sync.WaitGroup
}
type peer struct {
	watch         w.WatchResources
	lastState     *w.State
	revision      uint64
	identity      string
	conn          w.Connection
	stream        w.Stream
	member        w.Member
	epoch         uint32
	out           chan []byte
	voiceOut      *w.VoiceRing
	done          chan struct{}
	commandBucket bucket
	voiceBucket   bucket
}
type bucket struct {
	at     time.Time
	tokens float64
}

func (b *bucket) take(rate, burst float64) bool {
	now := time.Now()
	if b.at.IsZero() {
		b.tokens = burst
	} else {
		b.tokens = min(burst, b.tokens+now.Sub(b.at).Seconds()*rate)
	}
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func Listen(address string, cfg Config) (*Server, error) {
	if cfg.ChannelStore != nil {
		cfg.Channels = cfg.ChannelStore.record.Channels
	}
	if cfg.MaxClients == 0 {
		cfg.MaxClients = w.MaxMembers
	}
	if cfg.MaxClients < 1 || cfg.MaxClients > w.MaxMembers || !validName(cfg.Name, 100) || len(cfg.Password) > 1024 || len(cfg.Channels) == 0 || len(cfg.Channels) > w.MaxChannels {
		return nil, errors.New("invalid server configuration")
	}
	seen := map[uint16]bool{}
	channelBytes := 0
	for _, ch := range cfg.Channels {
		if ch.ID == 0 || seen[ch.ID] || !validName(ch.Name, 100) || !utf8.ValidString(ch.Description) || len(ch.Description) > 1024 {
			return nil, errors.New("invalid channel configuration")
		}
		seen[ch.ID] = true
		channelBytes += len(ch.Name) + len(ch.Description)
	}
	// Leave room for the largest bounded membership snapshot in a control frame.
	if channelBytes > 16*1024 {
		return nil, errors.New("channel configuration exceeds snapshot budget")
	}
	cfg.Channels = append([]w.Channel(nil), cfg.Channels...)
	l, err := w.ListenNoise(address, cfg.NoiseKey, cfg.MaxClients)
	if err != nil {
		return nil, err
	}
	return &Server{listener: l, config: cfg, peers: map[uint16]*peer{}}, nil
}
func (s *Server) Addr() net.Addr { return s.listener.Addr() }

// Serve owns every accepted connection and joins all relay workers on shutdown.
func (s *Server) Serve(ctx context.Context) error {
	ctx, cancelAll := context.WithCancel(ctx)
	slots := make(chan struct{}, s.config.MaxClients)
	stop := context.AfterFunc(ctx, func() { _ = s.listener.Close() })
	defer stop()
	defer func() { cancelAll(); _ = s.listener.Close(); s.wg.Wait() }()
	for {
		c, err := s.listener.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case slots <- struct{}{}:
		default:
			_ = c.CloseWithError(1, "server full")
			continue
		}
		s.wg.Add(1)
		go func() { defer s.wg.Done(); defer func() { <-slots }(); s.servePeer(ctx, c) }()
	}
}

func (s *Server) servePeer(ctx context.Context, c w.Connection) {
	stop := context.AfterFunc(ctx, func() { _ = c.CloseWithError(0, "server stopping") })
	defer stop()
	defer c.CloseWithError(0, "session ended")
	setup, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stream, err := c.AcceptStream(setup)
	if err != nil {
		return
	}
	_ = stream.SetDeadline(time.Now().Add(5 * time.Second))
	f, err := w.Read(stream)
	var hello w.Hello
	if err != nil || f.Kind != w.HelloKind || f.Request != 0 || w.Decode(f, &hello) != nil || !validName(hello.Nickname, 30) || len(hello.Password) > 1024 {
		return
	}
	got, want := sha256.Sum256([]byte(hello.Password)), sha256.Sum256([]byte(s.config.Password))
	if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
		_ = c.CloseWithError(w.AuthenticationFailed, "authentication failed")
		return
	}
	if w.VerifyHello(c, hello) != nil {
		_ = c.CloseWithError(w.AuthenticationFailed, "identity authentication failed")
		return
	}
	if !c.SupportsDatagrams() {
		return
	}
	_ = stream.SetDeadline(time.Time{})
	p := &peer{conn: c, stream: stream, epoch: 1, out: make(chan []byte, 16), voiceOut: w.NewVoiceRing(4), done: make(chan struct{})}
	p.identity = nativeidentity.UID(hello.PublicKey)
	// Bootstrap only the current channel; the client explicitly lists what its UI needs.
	s.mu.Lock()
	// IDs are never reused during this server lifetime, including after disconnect.
	if s.next >= 65535 {
		s.mu.Unlock()
		return
	}
	s.next++
	p.member = w.Member{ID: uint16(s.next), Channel: s.config.Channels[0].ID, Nickname: hello.Nickname, Instance: rand.Text(), Muted: true, Epoch: 1}
	s.peers[p.member.ID] = p
	s.sendStateLocked(p, w.WelcomeKind, true)
	s.broadcastLocked(p)
	member := p.member
	s.mu.Unlock()
	remoteAddr, remoteIP := "unknown", "unknown"
	if remote, ok := c.(interface{ RemoteAddr() net.Addr }); ok && remote.RemoteAddr() != nil {
		remoteAddr = remote.RemoteAddr().String()
		if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
			remoteIP = host
		}
	}
	logger := slog.With("remote_addr", remoteAddr, "remote_ip", remoteIP,
		"name", member.Nickname, "member_id", member.ID, "session", member.Instance)
	connectedAt := time.Now()
	logger.Info("client connected")
	var workers sync.WaitGroup
	workers.Add(3)
	go func() { defer workers.Done(); s.writeLoop(p) }()
	go func() { defer workers.Done(); s.voiceLoop(p) }()
	go func() { defer workers.Done(); w.SendVoiceRing(p.conn, p.voiceOut) }()
	defer func() {
		p.voiceOut.Close()
		_ = c.CloseWithError(0, "session ended")
		close(p.done)
		workers.Wait()
		s.mu.Lock()
		delete(s.peers, p.member.ID)
		s.broadcastLocked(nil)
		s.mu.Unlock()
		logger.Info("client disconnected", "duration_ms", time.Since(connectedAt).Milliseconds())
	}()
	for {
		f, err := w.Read(stream)
		if err != nil {
			return
		}
		if f.Request == 0 {
			return
		}
		if f.Kind == w.WatchResourcesKind {
			var watch w.WatchResources
			if w.Decode(f, &watch) != nil || (watch.AllMembers && !watch.AllChannels) {
				return
			}
			s.mu.Lock()
			if p.commandBucket.take(10, 20) {
				p.watch = watch
				s.sendStateLocked(p, w.StateKind, true)
				s.enqueueLocked(p, w.ReplyKind, f.Request, w.Reply{Code: w.OK})
			} else {
				s.enqueueLocked(p, w.ReplyKind, f.Request, w.Reply{Code: w.RateLimited})
			}
			s.mu.Unlock()
			continue
		}
		if f.Kind >= w.CreateChannelKind && f.Kind <= w.DeleteChannelKind {
			if !s.channelCommand(p, f) {
				return
			}
			continue
		}
		if f.Kind == w.ClaimOwnerKind {
			var claim w.ClaimOwner
			if w.Decode(f, &claim) != nil {
				return
			}
			if !p.commandBucket.take(1, 3) {
				s.mu.Lock()
				s.enqueueLocked(p, w.ReplyKind, f.Request, w.Reply{Code: w.RateLimited})
				s.mu.Unlock()
				continue
			}
			code := uint8(w.OK)
			if s.config.Ownership.Claim(p.identity, claim.Token) != nil {
				code = w.Rejected
			}
			s.mu.Lock()
			s.broadcastLocked(nil)
			s.enqueueLocked(p, w.ReplyKind, f.Request, w.Reply{Code: code})
			s.mu.Unlock()
			continue
		}
		var cmd w.Command
		if w.Decode(f, &cmd) != nil {
			return
		}
		s.mu.Lock()
		if !p.commandBucket.take(10, 20) {
			s.enqueueLocked(p, w.ReplyKind, f.Request, w.Reply{Code: w.RateLimited})
			s.mu.Unlock()
			continue
		}
		code := uint8(w.OK)
		switch f.Kind {
		case w.MoveKind:
			found := false
			for _, ch := range s.config.Channels {
				if ch.ID == cmd.Channel {
					found = true
				}
			}
			if !found || cmd.Channel == s.deletingChannel {
				code = w.WrongChannel
			} else if p.member.Channel != cmd.Channel {
				if p.epoch == ^uint32(0) {
					s.mu.Unlock()
					return
				}
				p.epoch++
				p.member.Epoch = p.epoch
				p.member.Channel = cmd.Channel
				s.broadcastLocked(nil)
			}
		case w.ChatKind:
			if cmd.Channel != p.member.Channel {
				code = w.WrongChannel
			} else if !utf8.ValidString(cmd.Text) || len(cmd.Text) > 8192 || strings.TrimSpace(cmd.Text) == "" || strings.ContainsRune(cmd.Text, 0) {
				code = w.Rejected
			} else {
				msg := w.Message{Channel: p.member.Channel, Sender: p.member.ID, Nickname: p.member.Nickname, Text: cmd.Text}
				for _, other := range s.peers {
					if other != p && other.member.Channel == p.member.Channel {
						s.enqueueLocked(other, w.MessageKind, 0, msg)
					}
				}
			}
		case w.VoiceStateKind:
			if p.member.Muted != cmd.Muted || p.member.Deafened != cmd.Deafened {
				p.member.Muted = cmd.Muted
				p.member.Deafened = cmd.Deafened
				s.broadcastLocked(nil)
			}
		default:
			s.mu.Unlock()
			return
		}
		s.enqueueLocked(p, w.ReplyKind, f.Request, w.Reply{Code: code})
		s.mu.Unlock()
	}
}

func (s *Server) stateLocked(p *peer) w.State {
	members := make([]w.Member, 0, len(s.peers))
	for _, v := range s.peers {
		if p.watch.AllMembers || v.member.Channel == p.member.Channel {
			members = append(members, v.member)
		}
	}
	sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
	role, canClaim := s.config.Ownership.Status(p.identity)
	channels := s.config.Channels
	if !p.watch.AllChannels {
		channels = nil
		for _, ch := range s.config.Channels {
			if ch.ID == p.member.Channel {
				channels = append(channels, ch)
			}
		}
	}
	return w.State{Name: s.config.Name, Self: p.member.ID, Epoch: p.epoch, Channels: channels, Members: members, IdentityUID: p.identity, ServerRole: role, CanClaimOwner: canClaim, CanManageChannels: role == "owner" && s.config.ChannelStore != nil, CanConfigureChannelAudio: role == "owner" && s.config.ChannelStore != nil, CanWatchResources: true, AllChannels: p.watch.AllChannels, AllMembers: p.watch.AllMembers, DefaultChannel: s.config.Channels[0].ID}
}

// A scoped snapshot replaces prior resource state. Reliable stream ordering plus
// a per-session revision avoids a gap between listing and starting the watch.
func (s *Server) sendStateLocked(p *peer, kind uint8, force bool) {
	state := s.stateLocked(p)
	if !force && p.lastState != nil && reflect.DeepEqual(*p.lastState, state) {
		return
	}
	p.revision++
	state.Revision = p.revision
	s.enqueueLocked(p, kind, 0, state)
	// Keep the comparison snapshot immutable and independent of the wire revision.
	copy := state
	copy.Revision = 0
	p.lastState = &copy
}

func (s *Server) broadcastLocked(except *peer) {
	for _, p := range s.peers {
		if p != except {
			s.sendStateLocked(p, w.StateKind, false)
		}
	}
}

func (s *Server) enqueueLocked(p *peer, kind uint8, id uint32, value any) {
	b, err := w.Pack(kind, id, value)
	if err != nil {
		_ = p.conn.CloseWithError(1, "state limit")
		return
	}
	select {
	case p.out <- b:
	default:
		_ = p.conn.CloseWithError(1, "slow control consumer")
	}
}

func (s *Server) writeLoop(p *peer) {
	for {
		select {
		case <-p.done:
			return
		case <-p.conn.Context().Done():
			return
		case b := <-p.out:
			_ = p.stream.SetWriteDeadline(time.Now().Add(5 * time.Second))
			for len(b) > 0 {
				n, err := p.stream.Write(b)
				if err != nil || n == 0 {
					_ = p.conn.CloseWithError(1, "control write failed")
					return
				}
				b = b[n:]
			}
		}
	}
}

func (s *Server) voiceLoop(p *peer) {
	type target struct {
		queue *w.VoiceRing
		epoch uint32
	}
	for {
		data, err := p.conn.ReceiveDatagram(p.conn.Context())
		if err != nil {
			return
		}
		at := time.Now()
		v, err := w.DecodeVoice(data, false)
		if err != nil {
			continue
		}
		s.mu.Lock()
		if v.Epoch != p.epoch || p.member.Muted || !p.voiceBucket.take(60, 10) {
			s.mu.Unlock()
			continue
		}
		// Snapshot membership under the state lock; encoding and enqueueing belong
		// to the source loop, outside the shared server critical section.
		var targets [w.MaxMembers]target
		count := 0
		sender, epoch := p.member.ID, p.epoch
		for _, other := range s.peers {
			if other == p || other.member.Channel != p.member.Channel || other.member.Deafened {
				continue
			}
			targets[count] = target{other.voiceOut, other.epoch}
			count++
		}
		s.mu.Unlock()
		for _, other := range targets[:count] {
			packet, _ := w.EncodeVoice(w.Voice{Epoch: other.epoch, SenderEpoch: epoch, Sender: sender, Sequence: v.Sequence, End: v.End, Data: v.Data}, true)
			other.queue.Push(w.QueuedVoice{Data: packet, At: at})
		}
	}
}

func validName(s string, maxRunes int) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= maxRunes && !strings.ContainsFunc(s, unicode.IsControl)
}
