package teamspeak

import (
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/honeybbq/teamspeak-go/crypto"
	"github.com/honeybbq/teamspeak-go/transport"
)

const testClientIdentity = "W2OSGpWxkzBPJjt8iyJFsMnqnwHCnxOlmE9gWFOFnKs=:0"

// testConn is a minimal io.ReadWriteCloser used for tests that only exercise
// in-memory logic. Writes are discarded; Read blocks until Close is called.
type testConn struct {
	buf  chan []byte
	done chan struct{}
	once sync.Once
}

func newTestConn() *testConn {
	return &testConn{
		buf:  make(chan []byte, 256),
		done: make(chan struct{}),
	}
}

func (c *testConn) Read(b []byte) (int, error) {
	select {
	case data, open := <-c.buf:
		if !open {
			return 0, io.EOF
		}
		n := copy(b, data)

		return n, nil
	case <-c.done:
		return 0, io.EOF
	}
}

func (c *testConn) Write(b []byte) (int, error) { return len(b), nil }

func (c *testConn) Close() error {
	c.once.Do(func() { close(c.done) })

	return nil
}

// newTestClient creates a Client whose PacketHandler is wired to a no-op testConn.
// Goroutines are started so the handler is fully functional. t.Cleanup shuts down.
func newTestClient(t *testing.T) *Client {
	t.Helper()
	id, err := crypto.IdentityFromString(testClientIdentity)
	if err != nil {
		t.Fatalf("IdentityFromString: %v", err)
	}
	c := NewClient(id, "127.0.0.1:9987", "TestBot")
	tc := newTestConn()
	startErr := c.handler.Start(tc)
	if startErr != nil {
		t.Fatalf("handler.Start: %v", startErr)
	}
	t.Cleanup(func() { _ = c.handler.Close() })

	return c
}

// pipePair is an in-memory io.ReadWriteCloser with datagram semantics.
// Used for tests that need to inject/read packets from the client handler.
type pipePair struct {
	recv   <-chan []byte
	send   chan<- []byte
	done   chan struct{}
	once   sync.Once
	closed atomic.Bool
}

func (p *pipePair) Read(b []byte) (int, error) {
	select {
	case data, ok := <-p.recv:
		if !ok {
			return 0, io.EOF
		}
		n := copy(b, data)

		return n, nil
	case <-p.done:
		return 0, io.EOF
	}
}

func (p *pipePair) Write(b []byte) (int, error) {
	if p.closed.Load() {
		return 0, io.ErrClosedPipe
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	select {
	case p.send <- cp:
		return len(b), nil
	case <-p.done:
		return 0, io.ErrClosedPipe
	}
}

func (p *pipePair) Close() error {
	p.once.Do(func() {
		p.closed.Store(true)
		close(p.done)
	})

	return nil
}

// newPipePair returns two connected pipePair ends:
// the first is given to PacketHandler.Start(), the second is the "server" side.
func newPipePair() (io.ReadWriteCloser, *pipePair) {
	toClient := make(chan []byte, 256)
	fromClient := make(chan []byte, 256)
	done := make(chan struct{})
	client := &pipePair{recv: toClient, send: fromClient, done: done}
	server := &pipePair{recv: fromClient, send: toClient, done: done}

	return client, server
}

// newTestClientWithPipe creates a Client wired to an in-memory pipePair.
// The returned server-side *pipePair lets tests read what the handler sends
// and inject S2C packets.
func newTestClientWithPipe(t *testing.T) (*Client, *pipePair) {
	t.Helper()
	id, err := crypto.IdentityFromString(testClientIdentity)
	if err != nil {
		t.Fatalf("IdentityFromString: %v", err)
	}
	c := NewClient(id, "127.0.0.1:9987", "TestBot")
	clientConn, serverConn := newPipePair()
	startErr := c.handler.Start(clientConn)
	if startErr != nil {
		t.Fatalf("handler.Start: %v", startErr)
	}
	t.Cleanup(func() { _ = c.handler.Close() })

	return c, serverConn
}

// Compile-time guard: PacketHandler.Start must accept an io.ReadWriteCloser.
var _ = (*transport.PacketHandler)(nil)
