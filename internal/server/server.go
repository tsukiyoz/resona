// Package server is the authoritative Resona native channel and relay service.
package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	w "github.com/tsukiyoz/resona/internal/nativewire"
)

type Config struct {
	NoiseKey   []byte
	Name       string
	Password   string
	Channels   []w.Channel
	MaxClients int
}
type Server struct {
	listener w.Listener
	config   Config
	mu       sync.Mutex
	peers    map[uint16]*peer
	next     uint32
	wg       sync.WaitGroup
}
type peer struct {
	conn          w.Connection
	stream        w.Stream
	member        w.Member
	epoch         uint32
	out           chan []byte
	voiceOut      chan w.QueuedVoice
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
func Listen(address string, cfg Config, tlsConfig *tls.Config) (*Server, error) {
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
	var l w.Listener
	var err error
	if len(cfg.NoiseKey) != 0 {
		l, err = w.ListenNoise(address, cfg.NoiseKey, cfg.MaxClients)
	} else {
		if tlsConfig == nil || len(tlsConfig.Certificates) == 0 {
			return nil, errors.New("TLS certificate required")
		}
		tc := tlsConfig.Clone()
		tc.MinVersion = tls.VersionTLS13
		tc.NextProtos = []string{w.ALPN}
		l, err = w.ListenQUIC(address, tc)
	}
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
	if !c.SupportsDatagrams() {
		return
	}
	_ = stream.SetDeadline(time.Time{})
	p := &peer{conn: c, stream: stream, epoch: 1, out: make(chan []byte, 16), voiceOut: make(chan w.QueuedVoice, 4), done: make(chan struct{})}
	s.mu.Lock()
	// IDs are never reused during this server lifetime, including after disconnect.
	if s.next >= 65535 {
		s.mu.Unlock()
		return
	}
	s.next++
	p.member = w.Member{ID: uint16(s.next), Channel: s.config.Channels[0].ID, Nickname: hello.Nickname, Instance: rand.Text(), Muted: true, Epoch: 1}
	s.peers[p.member.ID] = p
	s.enqueueLocked(p, w.WelcomeKind, 0, s.stateLocked(p))
	s.broadcastLocked(p)
	s.mu.Unlock()
	var workers sync.WaitGroup
	workers.Add(3)
	go func() { defer workers.Done(); s.writeLoop(p) }()
	go func() { defer workers.Done(); s.voiceLoop(p) }()
	go func() { defer workers.Done(); w.SendVoiceQueue(p.conn, p.voiceOut) }()
	defer func() {
		_ = c.CloseWithError(0, "session ended")
		close(p.done)
		workers.Wait()
		s.mu.Lock()
		delete(s.peers, p.member.ID)
		s.broadcastLocked(nil)
		s.mu.Unlock()
	}()
	for {
		f, err := w.Read(stream)
		if err != nil {
			return
		}
		if f.Request == 0 {
			return
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
			if !found {
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
		members = append(members, v.member)
	}
	sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
	return w.State{Name: s.config.Name, Self: p.member.ID, Epoch: p.epoch, Channels: s.config.Channels, Members: members}
}
func (s *Server) broadcastLocked(except *peer) {
	for _, p := range s.peers {
		if p != except {
			s.enqueueLocked(p, w.StateKind, 0, s.stateLocked(p))
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
	for {
		data, err := p.conn.ReceiveDatagram(p.conn.Context())
		if err != nil {
			return
		}
		v, err := w.DecodeVoice(data, false)
		if err != nil {
			continue
		}
		s.mu.Lock()
		if v.Epoch != p.epoch || p.member.Muted || !p.voiceBucket.take(60, 10) {
			s.mu.Unlock()
			continue
		}
		for _, other := range s.peers {
			if other == p || other.member.Channel != p.member.Channel || other.member.Deafened {
				continue
			}
			packet, _ := w.EncodeVoice(w.Voice{Epoch: other.epoch, SenderEpoch: p.epoch, Sender: p.member.ID, Sequence: v.Sequence, End: v.End, Data: v.Data}, true)
			select {
			case other.voiceOut <- w.QueuedVoice{Data: packet, At: time.Now()}:
			default:
			}
		}
		s.mu.Unlock()
	}
}
func validName(s string, maxRunes int) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= maxRunes && !strings.ContainsFunc(s, unicode.IsControl)
}
