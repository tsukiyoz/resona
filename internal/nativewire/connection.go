package nativewire

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	"github.com/tsukiyoz/resona/internal/noiseudp"
	"github.com/tsukiyoz/resona/internal/serverauth"
)

type Stream interface {
	io.Reader
	io.Writer
	SetDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
}

type Connection interface {
	Context() context.Context
	OpenStreamSync(context.Context) (Stream, error)
	AcceptStream(context.Context) (Stream, error)
	SendDatagram([]byte) error
	ReceiveDatagram(context.Context) ([]byte, error)
	CloseWithError(uint64, string) error
	SupportsDatagrams() bool
}

type Listener interface {
	Addr() net.Addr
	Accept(context.Context) (Connection, error)
	Close() error
}

func ChannelBinding(c Connection) ([]byte, error) {
	if v, ok := c.(interface{ ChannelBinding() []byte }); ok {
		return v.ChannelBinding(), nil
	}
	return nil, errors.New("transport has no identity binding")
}

type noiseConnection struct{ *noiseudp.Conn }

func (c noiseConnection) OpenStreamSync(ctx context.Context) (Stream, error) {
	return c.Conn, ctx.Err()
}

func (c noiseConnection) AcceptStream(ctx context.Context) (Stream, error) { return c.Conn, ctx.Err() }
func (c noiseConnection) SupportsDatagrams() bool                          { return true }

type noiseListener struct{ *noiseudp.Listener }

func (l noiseListener) Accept(ctx context.Context) (Connection, error) {
	c, err := l.Listener.Accept(ctx)
	if err != nil {
		return nil, err
	}
	return noiseConnection{c}, nil
}

func DialNoise(ctx context.Context, address string, key []byte) (Connection, error) {
	c, err := noiseudp.Dial(ctx, address, key)
	if err != nil {
		return nil, err
	}
	return noiseConnection{c}, nil
}

// DialServer authenticates the server before application Hello. A nil key is
// allowed only with a password; discovery alone never establishes trust.
func DialServer(ctx context.Context, address string, key []byte, password string) (Connection, error) {
	if len(key) == 0 && password == "" {
		return nil, serverauth.ErrAuthentication
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	c, err := DialNoise(ctx, address, key)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = c.CloseWithError(0, "authentication cancelled") })
	defer stop()
	stream, err := c.OpenStreamSync(ctx)
	if err == nil {
		deadline, _ := ctx.Deadline()
		_ = stream.SetDeadline(deadline)
		var binding []byte
		binding, err = ChannelBinding(c)
		if err == nil {
			err = serverauth.Authenticate(stream, binding, password, len(key) != 0)
		}
		_ = stream.SetDeadline(time.Time{})
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		_ = c.CloseWithError(AuthenticationFailed, "authentication failed")
		return nil, err
	}
	return c, nil
}

func ListenNoise(address string, key []byte, limit int) (Listener, error) {
	l, err := noiseudp.Listen(address, key, limit)
	if err != nil {
		return nil, err
	}
	return noiseListener{l}, nil
}
