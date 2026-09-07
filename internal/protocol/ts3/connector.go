// Package ts3 adapts the pinned TS3 transport to Resona's read-only sessions.
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
	s := &connection{state: newReducer(identityUID(id)), update: update, ready: make(chan struct{}), ended: make(chan struct{})}
	s.client = teamspeak.NewClient(id, endpoint, profile.Nickname,
		teamspeak.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		teamspeak.WithResolver(fixedResolver(endpoint)),
		teamspeak.WithServerPassword(password),
		teamspeak.WithCommandObserver(s.observe),
		teamspeak.WithCommandMiddleware(muteOutput))
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
		return s, nil
	}
}

type connection struct {
	mu        sync.Mutex
	client    *teamspeak.Client
	state     *reducer
	update    func(client.RemoteState)
	ready     chan struct{}
	ended     chan struct{}
	readyOnce sync.Once
	endOnce   sync.Once
	closeOnce sync.Once
	closing   bool
	closeErr  error
}

func (s *connection) observe(cmd teamspeak.IncomingCommand) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.state.state.Closed || !s.state.apply(cmd) {
		return
	}
	if s.state.state.Closed {
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
	s.state.state.Error = "与服务器的连接已断开"
	s.endOnce.Do(func() { close(s.ended) })
	s.update(s.state.snapshot())
}

func (s *connection) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		s.mu.Unlock()
		s.closeErr = s.client.Disconnect()
	})
	return s.closeErr
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
