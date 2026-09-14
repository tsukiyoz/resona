package nativewire

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/tsukiyoz/resona/internal/noiseudp"
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
type quicConnection struct{ *quic.Conn }

func ChannelBinding(c Connection) ([]byte, error) {
	switch v := c.(type) {
	case noiseConnection:
		return v.ChannelBinding(), nil
	case quicConnection:
		tlsState := v.ConnectionState().TLS
		return tlsState.ExportKeyingMaterial("EXPORTER-Resona-Identity-v1", nil, 32)
	default:
		return nil, errors.New("transport has no identity binding")
	}
}

func (c quicConnection) OpenStreamSync(ctx context.Context) (Stream, error) {
	return c.Conn.OpenStreamSync(ctx)
}
func (c quicConnection) AcceptStream(ctx context.Context) (Stream, error) {
	return c.Conn.AcceptStream(ctx)
}
func (c quicConnection) CloseWithError(code uint64, msg string) error {
	return c.Conn.CloseWithError(quic.ApplicationErrorCode(code), msg)
}
func (c quicConnection) SupportsDatagrams() bool { return c.ConnectionState().SupportsDatagrams.Remote }

type quicListener struct{ *quic.Listener }

func (l quicListener) Accept(ctx context.Context) (Connection, error) {
	c, e := l.Listener.Accept(ctx)
	if e != nil {
		return nil, e
	}
	return quicConnection{c}, nil
}
func DialQUIC(ctx context.Context, address string, tlsConfig *tls.Config) (Connection, error) {
	c, e := quic.DialAddr(ctx, address, tlsConfig, QUICConfig(false))
	if e != nil {
		return nil, e
	}
	return quicConnection{c}, nil
}
func ListenQUIC(address string, tlsConfig *tls.Config) (Listener, error) {
	l, e := quic.ListenAddr(address, tlsConfig, QUICConfig(true))
	if e != nil {
		return nil, e
	}
	return quicListener{l}, nil
}

type noiseConnection struct{ *noiseudp.Conn }

func (c noiseConnection) OpenStreamSync(ctx context.Context) (Stream, error) {
	return c.Conn, ctx.Err()
}
func (c noiseConnection) AcceptStream(ctx context.Context) (Stream, error) { return c.Conn, ctx.Err() }
func (c noiseConnection) SupportsDatagrams() bool                          { return true }

type noiseListener struct{ *noiseudp.Listener }

func (l noiseListener) Accept(ctx context.Context) (Connection, error) {
	c, e := l.Listener.Accept(ctx)
	if e != nil {
		return nil, e
	}
	return noiseConnection{c}, nil
}
func DialNoise(ctx context.Context, address string, key []byte) (Connection, error) {
	c, e := noiseudp.Dial(ctx, address, key)
	if e != nil {
		return nil, e
	}
	return noiseConnection{c}, nil
}
func ListenNoise(address string, key []byte, limit int) (Listener, error) {
	l, e := noiseudp.Listen(address, key, limit)
	if e != nil {
		return nil, e
	}
	return noiseListener{l}, nil
}
