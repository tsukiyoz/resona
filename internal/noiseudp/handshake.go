package noiseudp

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/flynn/noise"
)

const prologue = "resona-noise-exp-1"

var suite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)
var magic = []byte{'R', 'N', '0', '1'}

func PublicKey(private []byte) ([]byte, error) {
	k, err := ecdh.X25519().NewPrivateKey(private)
	if err != nil {
		return nil, err
	}
	return k.PublicKey().Bytes(), nil
}
func GenerateKey() ([]byte, error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return k.Bytes(), nil
}
func handshake(initiator bool, key []byte) (*noise.HandshakeState, error) {
	c := noise.Config{CipherSuite: suite, Pattern: noise.HandshakeNK, Initiator: initiator, Prologue: []byte(prologue)}
	if initiator {
		if len(key) != 32 {
			return nil, errors.New("Noise server public key required")
		}
		c.PeerStatic = key
	} else {
		pub, err := PublicKey(key)
		if err != nil {
			return nil, err
		}
		c.StaticKeypair = noise.DHKey{Private: key, Public: pub}
	}
	return noise.NewHandshakeState(c)
}

// NK sends no application data until both ephemeral contributions are verified.
// A stateless, address-bound cookie is required before server DH work/state.
func Dial(ctx context.Context, address string, public []byte) (*Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	nc, err := (&net.Dialer{}).DialContext(ctx, "udp", address)
	if err != nil {
		return nil, err
	}
	u := nc.(*net.UDPConn)
	success := false
	defer func() {
		if !success {
			_ = u.Close()
		}
	}()
	stop := context.AfterFunc(ctx, func() { _ = u.Close() })
	defer stop()
	hs, err := handshake(true, public)
	if err != nil {
		return nil, err
	}
	msg, _, _, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, err
	}
	request := append(append(append([]byte{}, magic...), 1), make([]byte, 20)...)
	request = append(request, msg...)
	buf := make([]byte, maxPacket+1)
	for ctx.Err() == nil {
		_ = u.SetDeadline(time.Now().Add(200 * time.Millisecond))
		if _, err = u.Write(request); err != nil {
			return nil, err
		}
		n, err := u.Read(buf)
		if err != nil {
			if e, ok := err.(net.Error); ok && e.Timeout() {
				continue
			}
			return nil, err
		}
		if n < 5 || !bytes.Equal(buf[:4], magic) {
			continue
		}
		if buf[4] == 2 && n == 25 {
			copy(request[5:25], buf[5:25])
			continue
		}
		if buf[4] != 3 || n != 53 {
			continue
		}
		_, tx, rx, err := hs.ReadMessage(nil, buf[5:n])
		if err != nil {
			return nil, errors.New("Noise server authentication failed")
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		_ = u.SetDeadline(time.Time{})
		c := newConn(tx, rx, u.LocalAddr(), u.RemoteAddr(), func(b []byte) error {
			_ = u.SetWriteDeadline(time.Now().Add(250 * time.Millisecond))
			_, e := u.Write(b)
			return e
		}, func() { _ = u.Close() })
		go func() {
			defer c.fail(net.ErrClosed)
			for {
				n, e := u.Read(buf)
				if e != nil {
					return
				}
				c.receive(buf[:n])
			}
		}()
		success = true
		return c, nil
	}
	return nil, ctx.Err()
}

type entry struct {
	conn              *Conn
	request, response []byte
}
type Listener struct {
	u           *net.UDPConn
	key, secret []byte
	mu          sync.Mutex
	closeOnce   sync.Once
	stopping    bool
	workers     sync.WaitGroup
	peers       map[string]*entry
	accept      chan *Conn
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
	writeGate   chan struct{}
	limit       int
	lastDH      time.Time
	dhTokens    float64
}

func Listen(address string, private []byte, limit int) (*Listener, error) {
	if _, err := PublicKey(private); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 64 {
		return nil, errors.New("invalid Noise connection limit")
	}
	a, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, err
	}
	u, err := net.ListenUDP("udp", a)
	if err != nil {
		return nil, err
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		_ = u.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	l := &Listener{u: u, key: append([]byte(nil), private...), secret: secret, peers: map[string]*entry{}, accept: make(chan *Conn, limit), ctx: ctx, cancel: cancel, done: make(chan struct{}), writeGate: make(chan struct{}, 1), limit: limit, dhTokens: 20, lastDH: time.Now()}
	go l.loop()
	return l, nil
}
func (l *Listener) Addr() net.Addr { return l.u.LocalAddr() }
func (l *Listener) Accept(ctx context.Context) (*Conn, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.ctx.Done():
		return nil, net.ErrClosed
	case c := <-l.accept:
		return c, nil
	}
}
func (l *Listener) Close() error {
	l.closeOnce.Do(func() {
		deadline := time.AfterFunc(time.Second, func() { l.cancel(); _ = l.u.Close() })
		defer deadline.Stop()
		l.mu.Lock()
		l.stopping = true
		all := make([]*Conn, 0, len(l.peers))
		for _, p := range l.peers {
			all = append(all, p.conn)
		}
		l.mu.Unlock()
		for _, c := range all {
			_ = c.CloseWithError(0, "server stopping")
		}
		l.cancel()
		_ = l.u.Close()
	})
	<-l.done
	return nil
}
func (l *Listener) write(b []byte, addr *net.UDPAddr) error {
	t := time.NewTimer(250 * time.Millisecond)
	defer t.Stop()
	select {
	case l.writeGate <- struct{}{}:
	case <-l.ctx.Done():
		return net.ErrClosed
	case <-t.C:
		return errors.New("UDP writer busy")
	}
	defer func() { <-l.writeGate }()
	_ = l.u.SetWriteDeadline(time.Now().Add(250 * time.Millisecond))
	_, err := l.u.WriteToUDP(b, addr)
	return err
}
func (l *Listener) cookie(addr string, msg []byte, slot uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, slot)
	h := hmac.New(sha256.New, l.secret)
	h.Write([]byte(prologue))
	h.Write([]byte(addr))
	h.Write(b)
	h.Write(msg)
	return append(b, h.Sum(nil)[:16]...)
}
func (l *Listener) loop() {
	defer close(l.done)
	defer func() {
		l.cancel()
		l.mu.Lock()
		all := make([]*Conn, 0, len(l.peers))
		for _, p := range l.peers {
			all = append(all, p.conn)
		}
		l.mu.Unlock()
		for _, c := range all {
			c.fail(net.ErrClosed)
			<-c.done
		}
		l.workers.Wait()
	}()
	buf := make([]byte, maxPacket+1)
	for {
		n, addr, err := l.u.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n > maxPacket {
			continue
		}
		b := buf[:n]
		name := addr.String()
		l.mu.Lock()
		p := l.peers[name]
		l.mu.Unlock()
		if n >= 5 && bytes.Equal(b[:4], magic) && b[4] == 1 {
			if n != 73 {
				continue
			}
			if p != nil {
				if bytes.Equal(b, p.request) {
					_ = l.write(p.response, addr)
				}
				continue
			}
			slot := uint32(time.Now().Unix() / 30)
			supplied := binary.BigEndian.Uint32(b[5:9])
			if (supplied != slot && supplied != slot-1) || !hmac.Equal(b[5:25], l.cookie(name, b[25:], supplied)) {
				response := append(append(append([]byte{}, magic...), 2), l.cookie(name, b[25:], slot)...)
				_ = l.write(response, addr)
				continue
			}
			l.mu.Lock()
			full := l.stopping || len(l.peers) >= l.limit
			l.mu.Unlock()
			if full {
				continue
			}
			now := time.Now()
			l.dhTokens = min(20, l.dhTokens+now.Sub(l.lastDH).Seconds()*10)
			l.lastDH = now
			if l.dhTokens < 1 {
				continue
			}
			l.dhTokens--
			hs, e := handshake(false, l.key)
			if e != nil {
				continue
			}
			if _, _, _, e = hs.ReadMessage(nil, b[25:]); e != nil {
				continue
			}
			response, rx, tx, e := hs.WriteMessage(nil, nil)
			if e != nil {
				continue
			}
			response = append(append(append([]byte{}, magic...), 3), response...)
			c := newConn(tx, rx, l.Addr(), addr, func(packet []byte) error { return l.write(packet, addr) }, func() { l.mu.Lock(); delete(l.peers, name); l.mu.Unlock() })
			l.workers.Add(1)
			go func() { defer l.workers.Done(); <-c.done }()
			l.mu.Lock()
			l.peers[name] = &entry{conn: c, request: append([]byte(nil), b...), response: response}
			l.mu.Unlock()
			if e = l.write(response, addr); e != nil {
				c.fail(e)
				continue
			}
			select {
			case l.accept <- c:
			default:
				c.fail(errors.New("accept queue full"))
			}
		} else if p != nil {
			p.conn.receive(b)
		}
	}
}
