package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
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
	directory       string
	server          *natsserver.Server
	conn            *nats.Conn
	js              jetstream.JetStream
	stream          jetstream.Stream
	temporary       string
	closed          chan struct{}
	retentionMu     sync.Mutex
	initialSettings ServerSettings
}

func openChat(cfg ChatConfig) (_ *chatStore, err error) {
	return openChatConfigured(cfg, false)
}

func openChatConfigured(cfg ChatConfig, preserveExisting bool) (_ *chatStore, err error) {
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
	c := &chatStore{closed: make(chan struct{}), initialSettings: ServerSettings{ChatMaxMessages: cfg.MaxMessagesPerChannel, ChatMaxBytes: cfg.MaxBytes, ChatMaxAgeSeconds: int64(cfg.MaxAge / time.Second)}}
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
	c.directory = cfg.Directory
	c.server, err = natsserver.NewServer(&natsserver.Options{
		DontListen: true, NoSigs: true, NoLog: true, NoSystemAccount: true,
		JetStream: true, StoreDir: cfg.Directory, JetStreamMaxMemory: 8 << 20,
		// Stream MaxBytes owns retention. A broker quota equal to a newly reduced
		// limit would reject recovery before the stream could evict old messages.
		MaxPayload: 256 << 10, MaxPending: 1 << 20,
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
		nats.ClosedHandler(func(*nats.Conn) { close(c.closed) }),
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
	if preserveExisting {
		c.stream, err = c.js.Stream(ctx, "RESONA_CHAT")
		if err == nil {
			info := c.stream.CachedInfo()
			if !info.Config.AllowDirect {
				cfg := info.Config
				cfg.AllowDirect = true
				c.stream, err = c.js.UpdateStream(ctx, cfg)
			}
			return c, err
		}
		if !errors.Is(err, jetstream.ErrStreamNotFound) {
			return nil, err
		}
	}
	c.stream, err = c.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name: "RESONA_CHAT", Subjects: []string{"resona.chat.channel.*"},
		Storage: jetstream.FileStorage, Retention: jetstream.LimitsPolicy,
		Discard: jetstream.DiscardOld, MaxMsgsPerSubject: cfg.MaxMessagesPerChannel,
		MaxBytes: cfg.MaxBytes, MaxAge: cfg.MaxAge, MaxMsgSize: 12 << 10,
		MaxConsumers: w.MaxMembers * 2, Duplicates: min(2*time.Minute, cfg.MaxAge),
		AllowDirect: true,
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

// NATS' pinned direct multi-last API resolves the previous retained subject
// sequence, even when other channels or retention create gaps. No body cache or
// application-maintained sequence index is needed. All reads stay off voice locks.
func (c *chatStore) previous(ctx context.Context, channel uint16, before uint64) (*w.Message, error) {
	if before == 1 {
		return nil, nil
	}
	request := struct {
		Subjects []string `json:"multi_last"`
		UpTo     uint64   `json:"up_to_seq,omitempty"`
		Batch    int      `json:"batch"`
	}{Subjects: []string{chatSubject(channel)}, Batch: 1}
	if before != 0 {
		request.UpTo = before - 1
	}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	msg, err := c.conn.RequestWithContext(ctx, "$JS.API.DIRECT.GET.RESONA_CHAT", data)
	if err != nil {
		return nil, err
	}
	if msg.Header.Get("Status") == "404" {
		return nil, nil
	}
	if msg.Header.Get("Status") != "" || msg.Header.Get("Nats-Subject") != chatSubject(channel) {
		return nil, errors.New("chat history read failed")
	}
	sequence, err := strconv.ParseUint(msg.Header.Get("Nats-Sequence"), 10, 64)
	if err != nil || sequence == 0 || (before != 0 && sequence >= before) {
		return nil, errors.New("invalid chat history cursor")
	}
	timestamp, err := time.Parse(time.RFC3339Nano, msg.Header.Get("Nats-Time-Stamp"))
	if err != nil {
		return nil, err
	}
	frame, err := w.Read(bytes.NewReader(msg.Data))
	var value w.Message
	if err != nil || w.Decode(frame, &value) != nil || value.Channel != channel {
		return nil, errors.New("invalid stored chat message")
	}
	value.Sequence, value.Timestamp, value.History = sequence, timestamp.UnixNano(), true
	return &value, nil
}

func (c *chatStore) page(ctx context.Context, channel uint16, before uint64) (w.ChatPage, error) {
	page := w.ChatPage{Channel: channel, Before: before}
	cursor, size := before, 0
	for {
		msg, err := c.previous(ctx, channel, cursor)
		if err != nil {
			return page, err
		}
		if msg == nil {
			break
		}
		// Leave room for page fields and epoch/self metadata added by the caller.
		n := len(msg.Text) + len(msg.Nickname) + len(msg.AuthorUID) + len(msg.ClientID) + 128
		if len(page.Messages) == 20 || size+n > 48<<10 {
			page.HasMore = true
			break
		}
		page.Messages = append(page.Messages, *msg)
		size += n
		cursor = msg.Sequence
		page.NextBefore = cursor
	}
	slices.Reverse(page.Messages)
	return page, nil
}

// Initial history and live delivery share a high-water mark. Messages committed
// while the page is read are consumed starting at boundary+1, without a gap.
func (c *chatStore) readPage(ctx context.Context, channel uint16, emit func(w.Message) error, ready func(w.ChatPage) error) error {
	setup, cancel := context.WithTimeout(ctx, 2*time.Second)
	last, err := c.stream.GetLastMsgForSubject(setup, chatSubject(channel))
	var boundary uint64
	if err == nil {
		boundary = last.Sequence
	} else if !errors.Is(err, jetstream.ErrMsgNotFound) {
		cancel()
		return err
	}
	var page w.ChatPage
	if boundary != 0 {
		page, err = c.page(setup, channel, boundary+1)
		if err != nil {
			cancel()
			return err
		}
	}
	consumer, err := c.stream.CreateConsumer(setup, jetstream.ConsumerConfig{
		FilterSubject: chatSubject(channel), DeliverPolicy: jetstream.DeliverByStartSequencePolicy, OptStartSeq: boundary + 1,
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
	for _, msg := range page.Messages {
		if err = emit(msg); err != nil {
			return err
		}
	}
	if err = ready(page); err != nil {
		return err
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
	}
}

func (c *chatStore) read(ctx context.Context, channel uint16, emit func(w.Message) error, ready func() error) error {
	return c.readPage(ctx, channel, emit, func(w.ChatPage) error { return ready() })
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
		author := p.authorUID()
		ctx, cancel := context.WithCancel(p.conn.Context())
		p.chatCancel = cancel
		s.mu.Unlock()
		send := func(kind uint8, value any) error {
			s.mu.Lock()
			current := !p.removed && s.peers[p.member.ID] == p && p.epoch == epoch && p.chatGeneration == generation
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
		err := s.chat.readPage(ctx, channel, func(msg w.Message) error {
			msg.Epoch, msg.Self = epoch, msg.AuthorUID == author
			return send(w.MessageKind, msg)
		}, func(page w.ChatPage) error {
			return send(w.ChatStatusKind, w.ChatStatus{Channel: channel, Epoch: epoch, Generation: generation, Status: 1, Before: page.NextBefore, HasMore: page.HasMore})
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
	if p.removed {
		code = w.PermissionDenied
	} else if !p.commandBucket.take(10, 20) {
		code = w.RateLimited
	} else if cmd.Channel != p.member.Channel {
		code = w.WrongChannel
	} else if !utf8.ValidString(cmd.Text) || len(cmd.Text) > 8192 || strings.TrimSpace(cmd.Text) == "" || strings.ContainsRune(cmd.Text, 0) || !validMessageID(cmd.MessageID) {
		code = w.Rejected
	}
	msg := w.Message{Channel: p.member.Channel, Sender: p.member.ID, Nickname: p.member.Nickname, Text: cmd.Text, AuthorUID: p.authorUID(), ClientID: cmd.MessageID}
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

func (s *Server) chatHistory(p *peer, f w.Frame, cmd w.Command) {
	s.mu.Lock()
	code := uint8(w.OK)
	if p.removed {
		code = w.PermissionDenied
	} else if !p.commandBucket.take(10, 20) {
		code = w.RateLimited
	} else if cmd.Channel != p.member.Channel || cmd.Epoch != p.epoch || cmd.Generation != p.chatGeneration {
		code = w.WrongChannel
	}
	author := p.authorUID()
	s.mu.Unlock()
	if code == w.OK {
		ctx, cancel := context.WithTimeout(p.conn.Context(), 2*time.Second)
		defer cancel()
		page, err := s.chat.page(ctx, cmd.Channel, cmd.Before)
		if err == nil {
			page.Epoch, page.Generation = cmd.Epoch, cmd.Generation
			for i := range page.Messages {
				page.Messages[i].Epoch = cmd.Epoch
				page.Messages[i].Self = page.Messages[i].AuthorUID == author
			}
			s.mu.Lock()
			current := !p.removed && p.epoch == cmd.Epoch && p.chatGeneration == cmd.Generation && p.member.Channel == cmd.Channel
			s.mu.Unlock()
			if !current {
				code = w.WrongChannel
			} else if data, packErr := w.Pack(w.ChatPageKind, f.Request, page); packErr == nil {
				select {
				case p.chatOut <- data:
					return
				case <-ctx.Done():
				}
			}
		}
		if code == w.OK {
			code = w.StorageFailed
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
