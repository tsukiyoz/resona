// Package ts3 adapts the pinned TS3 transport to Resona's client sessions.
package ts3

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	teamspeak "github.com/honeybbq/teamspeak-go"
	"github.com/honeybbq/teamspeak-go/commands"
	"github.com/honeybbq/teamspeak-go/discovery"
	"github.com/tsukiyoz/resona/internal/client"
)

type Connector struct{ identityPath string }

func New(identityPath string) *Connector { return &Connector{identityPath: identityPath} }

func NewDefault() (*Connector, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	return New(filepath.Join(dir, "resona", "identity.key")), nil
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
		teamspeak.WithCommandMiddleware(muteOutput))
	s.execCommand = s.client.ExecCommandContext
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
	mu               sync.Mutex
	client           *teamspeak.Client
	state            *reducer
	update           func(client.RemoteState)
	ready            chan struct{}
	ended            chan struct{}
	readyOnce        sync.Once
	endOnce          sync.Once
	closeOnce        sync.Once
	closing          bool
	closeErr         error
	changed          chan struct{}
	commandGate      chan struct{}
	lifetime         context.Context
	stop             context.CancelFunc
	execCommand      func(context.Context, string) error
	subscribeStarted bool
	background       sync.WaitGroup
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
	})
	return s.closeErr
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

func muteOutput(next func(string) error) func(string) error {
	return func(raw string) error {
		cmd := commands.ParseCommand(raw)
		if cmd != nil && cmd.Name == "clientupdate" {
			cmd.Params["client_input_muted"] = "1"
			cmd.Params["client_output_muted"] = "1"
			raw = commands.BuildCommand(cmd.Name, cmd.Params)
		}
		return next(raw)
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
