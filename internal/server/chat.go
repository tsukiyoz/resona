package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	w "github.com/tsukiyoz/resona/internal/nativewire"
)

// Limits apply to retained chat data, not the physical size of JetStream files.
type ChatConfig struct {
	Directory             string
	MaxMessagesPerChannel int64
	MaxBytes              int64
	MaxAge                time.Duration
}

type chatStore struct {
	server    *natsserver.Server
	conn      *nats.Conn
	js        jetstream.JetStream
	stream    jetstream.Stream
	temporary string
}

func openChat(cfg ChatConfig) (_ *chatStore, err error) {
	if cfg.MaxMessagesPerChannel == 0 {
		cfg.MaxMessagesPerChannel = 200
	}
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = 64 << 20
	}
	if cfg.MaxAge == 0 {
		cfg.MaxAge = 7 * 24 * time.Hour
	}
	if cfg.MaxMessagesPerChannel < 1 || cfg.MaxMessagesPerChannel > 10000 || cfg.MaxBytes < 16<<10 || cfg.MaxAge < time.Second {
		return nil, errors.New("invalid chat retention limits")
	}
	c := &chatStore{}
	if cfg.Directory == "" {
		cfg.Directory, err = os.MkdirTemp("", "resona-chat-")
		if err != nil {
			return nil, err
		}
		c.temporary = cfg.Directory
	}
	defer func() {
		if err != nil {
			c.close()
		}
	}()
	if err = os.MkdirAll(cfg.Directory, 0o700); err != nil {
		return nil, err
	}
	c.server, err = natsserver.NewServer(&natsserver.Options{
		DontListen: true, NoSigs: true, NoLog: true, NoSystemAccount: true,
		JetStream: true, StoreDir: cfg.Directory, JetStreamMaxMemory: 8 << 20,
		// Stream MaxBytes owns retention. A broker quota equal to a newly reduced
		// limit would reject recovery before the stream could evict old messages.
		MaxPayload: 16 << 10, MaxPending: 1 << 20,
		SyncAlways: true,
	})
	if err != nil {
		return nil, err
	}
	c.server.Start()
	if !c.server.ReadyForConnections(2 * time.Second) {
		return nil, errors.New("chat broker did not start")
	}
	c.conn, err = nats.Connect("", nats.InProcessServer(c.server), nats.NoReconnect(),
		nats.Timeout(2*time.Second), nats.FlusherTimeout(2*time.Second),
		nats.ErrorHandler(func(*nats.Conn, *nats.Subscription, error) { slog.Warn("chat broker subscription failed") }))
	if err != nil {
		return nil, err
	}
	c.js, err = jetstream.New(c.conn)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c.stream, err = c.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name: "RESONA_CHAT", Subjects: []string{"resona.chat.channel.*"},
		Storage: jetstream.FileStorage, Retention: jetstream.LimitsPolicy,
		Discard: jetstream.DiscardOld, MaxMsgsPerSubject: cfg.MaxMessagesPerChannel,
		MaxBytes: cfg.MaxBytes, MaxAge: cfg.MaxAge, MaxMsgSize: 12 << 10,
		MaxConsumers: w.MaxMembers * 2, Duplicates: min(2*time.Minute, cfg.MaxAge),
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (c *chatStore) close() {
	if c.conn != nil {
		c.conn.Close()
	}
	if c.server != nil {
		c.server.Shutdown()
		c.server.WaitForShutdown()
	}
	if c.temporary != "" {
		_ = os.RemoveAll(c.temporary)
	}
}

func chatSubject(channel uint16) string { return "resona.chat.channel." + strconv.Itoa(int(channel)) }

func (c *chatStore) publish(ctx context.Context, msg w.Message) error {
	data, err := w.Pack(w.MessageKind, 0, msg)
	if err != nil {
		return err
	}
	// Namespace client IDs by authenticated identity and channel, never nickname.
	id := sha256.Sum256([]byte(msg.AuthorUID + ":" + chatSubject(msg.Channel) + ":" + msg.ClientID))
	_, err = c.js.Publish(ctx, chatSubject(msg.Channel), data, jetstream.WithMsgID(hex.EncodeToString(id[:])))
	return err
}

// One ephemeral pull consumer spans catch-up and live delivery in stream order.
// No snapshot/live handoff gap, permanent per-user cursor, or voice-path I/O.
func (c *chatStore) read(ctx context.Context, channel uint16, emit func(w.Message) error, ready func() error) error {
	setup, cancel := context.WithTimeout(ctx, 2*time.Second)
	last, err := c.stream.GetLastMsgForSubject(setup, chatSubject(channel))
	var boundary uint64
	if err == nil {
		boundary = last.Sequence
	} else if !errors.Is(err, jetstream.ErrMsgNotFound) {
		cancel()
		return err
	}
	consumer, err := c.stream.CreateConsumer(setup, jetstream.ConsumerConfig{
		FilterSubject: chatSubject(channel), DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy: jetstream.AckNonePolicy, ReplayPolicy: jetstream.ReplayInstantPolicy,
		MemoryStorage: true, InactiveThreshold: 30 * time.Second,
		MaxWaiting: 2, MaxRequestBatch: 4, MaxRequestMaxBytes: 48 << 10,
	})
	cancel()
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = c.stream.DeleteConsumer(cleanup, consumer.CachedInfo().Name)
	}()
	messages, err := consumer.Messages(jetstream.PullMaxMessagesWithBytesLimit(4, 48<<10), jetstream.PullExpiry(30*time.Second))
	if err != nil {
		return err
	}
	defer messages.Stop()
	loading := boundary != 0 && consumer.CachedInfo().NumPending != 0
	if !loading {
		if err = ready(); err != nil {
			return err
		}
	}
	for {
		msg, err := messages.Next(jetstream.NextContext(ctx))
		if err != nil {
			return err
		}
		metadata, err := msg.Metadata()
		if err != nil {
			return err
		}
		frame, err := w.Read(bytes.NewReader(msg.Data()))
		var value w.Message
		if err != nil || w.Decode(frame, &value) != nil || value.Channel != channel {
			return errors.New("invalid stored chat message")
		}
		value.Sequence, value.Timestamp = metadata.Sequence.Stream, metadata.Timestamp.UnixNano()
		value.History = value.Sequence <= boundary
		if err = emit(value); err != nil {
			return err
		}
		if loading && (value.Sequence >= boundary || metadata.NumPending == 0) {
			loading = false
			if err = ready(); err != nil {
				return err
			}
		}
	}
}

func (s *Server) restartChatLocked(p *peer) {
	p.chatGeneration++
	if p.chatCancel != nil {
		p.chatCancel()
	}
	s.enqueueLocked(p, w.ChatStatusKind, 0, w.ChatStatus{Channel: p.member.Channel, Epoch: p.epoch, Generation: p.chatGeneration})
	select {
	case p.chatWake <- struct{}{}:
	default:
	}
}

func (s *Server) chatLoop(p *peer) {
	for {
		select {
		case <-p.conn.Context().Done():
			return
		case <-p.chatWake:
		}
		s.mu.Lock()
		channel, epoch, generation := p.member.Channel, p.epoch, p.chatGeneration
		ctx, cancel := context.WithCancel(p.conn.Context())
		p.chatCancel = cancel
		s.mu.Unlock()
		send := func(kind uint8, value any) error {
			s.mu.Lock()
			current := s.peers[p.member.ID] == p && p.epoch == epoch && p.chatGeneration == generation
			s.mu.Unlock()
			if !current || ctx.Err() != nil {
				return context.Canceled
			}
			data, err := w.Pack(kind, 0, value)
			if err != nil {
				return err
			}
			// Backpressure is bounded and outside the shared membership/voice lock.
			timer := time.NewTimer(2 * time.Second)
			defer timer.Stop()
			select {
			case p.chatOut <- data:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return errors.New("chat delivery stalled")
			}
		}
		err := s.chat.read(ctx, channel, func(msg w.Message) error {
			msg.Epoch, msg.Self = epoch, msg.AuthorUID == p.identity
			return send(w.MessageKind, msg)
		}, func() error {
			return send(w.ChatStatusKind, w.ChatStatus{Channel: channel, Epoch: epoch, Generation: generation, Status: 1})
		})
		if ctx.Err() == nil && err != nil {
			slog.Warn("channel chat unavailable", "channel", channel, "member_id", p.member.ID)
			_ = send(w.ChatStatusKind, w.ChatStatus{Channel: channel, Epoch: epoch, Generation: generation, Status: 2})
		}
		cancel()
		s.mu.Lock()
		if p.chatGeneration == generation {
			p.chatCancel = nil
		}
		s.mu.Unlock()
	}
}

func (s *Server) chatCommand(p *peer, f w.Frame, cmd w.Command) {
	s.mu.Lock()
	code := uint8(w.OK)
	if !p.commandBucket.take(10, 20) {
		code = w.RateLimited
	} else if cmd.Channel != p.member.Channel {
		code = w.WrongChannel
	} else if !utf8.ValidString(cmd.Text) || len(cmd.Text) > 8192 || strings.TrimSpace(cmd.Text) == "" || strings.ContainsRune(cmd.Text, 0) || !validMessageID(cmd.MessageID) {
		code = w.Rejected
	}
	msg := w.Message{Channel: p.member.Channel, Sender: p.member.ID, Nickname: p.member.Nickname, Text: cmd.Text, AuthorUID: p.identity, ClientID: cmd.MessageID}
	s.mu.Unlock()
	if code == w.OK {
		ctx, cancel := context.WithTimeout(p.conn.Context(), 2*time.Second)
		err := s.chat.publish(ctx, msg)
		cancel()
		if err != nil {
			code = w.DeliveryUnknown
			var apiError *jetstream.APIError
			if errors.As(err, &apiError) {
				code = w.StorageFailed
			}
			slog.Warn("chat persistence not confirmed", "channel", msg.Channel, "member_id", msg.Sender)
		}
	}
	s.mu.Lock()
	s.enqueueLocked(p, w.ReplyKind, f.Request, w.Reply{Code: code})
	s.mu.Unlock()
}

func validMessageID(id string) bool {
	if len(id) < 8 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
