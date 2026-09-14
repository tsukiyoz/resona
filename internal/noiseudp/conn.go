// Package noiseudp provides the experimental Resona Noise datagram session.
package noiseudp

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/flynn/noise"
)

const (
	maxPacket  = 1200
	maxChunk   = 1000
	maxCounter = 1 << 24
	voice      = byte(4)
	control    = byte(5)
	ack        = byte(6)
	ping       = byte(7)
	pong       = byte(8)
	closing    = byte(9)
	keyUpdate  = byte(10)
	keyACK     = byte(11)
	keyPhase   = byte(0x80)
)

type RemoteError struct{ Code uint64 }

func (e *RemoteError) Error() string { return "Noise peer closed the session" }

// replayWindow is updated only after successful authentication.
type replayWindow struct {
	highest uint32
	bits    uint64
}

func (r *replayWindow) seen(n uint32) bool {
	return n == 0 || n >= maxCounter || (n <= r.highest && (r.highest-n >= 64 || r.bits&(uint64(1)<<(r.highest-n)) != 0))
}
func (r *replayWindow) add(n uint32) {
	if n > r.highest {
		r.bits <<= n - r.highest
		r.highest = n
	}
	r.bits |= uint64(1) << (r.highest - n)
}

type Conn struct {
	ctx                   context.Context
	cancel                context.CancelFunc
	sendRaw               func([]byte) error
	onClose               func()
	local, remote         net.Addr
	tx, rx                noise.Cipher
	txState, rxState      *noise.CipherState
	txEpoch, rxEpoch      uint64
	txConfirmed           bool
	keySince, updateSent  time.Time
	rxNext, rxPrevious    noise.Cipher
	previousReplay        replayWindow
	previousUntil         time.Time
	sendMu                sync.Mutex
	counter               uint32
	replay                replayWindow // owned by the UDP read loop
	expected              uint32
	recv                  chan []byte
	voices                chan []byte
	acks                  chan uint32
	readMu                sync.Mutex
	pending               []byte
	writeGate             chan struct{}
	writeSeq              uint32
	mu                    sync.Mutex
	rd, wd                time.Time
	changed               chan struct{}
	err                   error
	lastReceive, lastSend atomic.Int64
	done                  chan struct{}
}

func newConn(tx, rx *noise.CipherState, local, remote net.Addr, send func([]byte) error, closed func()) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{ctx: ctx, cancel: cancel, tx: tx.Cipher(), rx: rx.Cipher(), txState: tx, rxState: rx,
		txConfirmed: true, keySince: time.Now(), local: local, remote: remote,
		sendRaw: send, onClose: closed, recv: make(chan []byte, 32), voices: make(chan []byte, 4), acks: make(chan uint32, 8),
		writeGate: make(chan struct{}, 1), changed: make(chan struct{}), done: make(chan struct{}), expected: 1}
	rx.Rekey()
	c.rxNext = rx.Cipher()
	c.lastReceive.Store(time.Now().UnixNano())
	c.lastSend.Store(time.Now().UnixNano())
	go c.maintain()
	return c
}
func (c *Conn) Context() context.Context { return c.ctx }
func (c *Conn) LocalAddr() net.Addr      { return c.local }
func (c *Conn) RemoteAddr() net.Addr     { return c.remote }
func (c *Conn) failure() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	return net.ErrClosed
}
func (c *Conn) fail(err error) {
	c.mu.Lock()
	first := c.err == nil
	if first {
		c.err = err
		c.cancel()
	}
	c.mu.Unlock()
	if first && c.onClose != nil {
		c.onClose()
	}
}
func (c *Conn) CloseWithError(code uint64, _ string) error {
	if c.ctx.Err() == nil {
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], code)
		_ = c.send(closing, b[:])
		c.fail(net.ErrClosed)
	}
	return nil
}
func (c *Conn) Close() error { _ = c.CloseWithError(0, ""); <-c.done; return nil }
func (c *Conn) maintain() {
	defer close(c.done)
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case now := <-t.C:
			if now.Sub(time.Unix(0, c.lastReceive.Load())) >= 30*time.Second {
				c.fail(errors.New("Noise session expired"))
				return
			}
			if err := c.refreshKeys(now); err != nil {
				return
			}
			if now.Sub(time.Unix(0, c.lastSend.Load())) >= 10*time.Second {
				if err := c.send(ping, nil); err != nil {
					c.fail(err)
					return
				}
			}
		}
	}
}
func (c *Conn) send(kind byte, body []byte) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if len(body)+21 > maxPacket {
		return errors.New("Noise datagram too large")
	}
	if err := c.prepareKeysLocked(time.Now()); err != nil {
		return err
	}
	return c.sendLocked(kind, body)
}

func (c *Conn) sendLocked(kind byte, body []byte) error {
	if c.ctx.Err() != nil {
		return c.failure()
	}
	if c.counter+1 >= maxCounter {
		c.fail(errors.New("Noise key packet budget exhausted"))
		return c.failure()
	}
	if len(body)+21 > maxPacket {
		return errors.New("Noise datagram too large")
	}
	c.counter++
	header := make([]byte, 5, 5+len(body)+16)
	header[0] = kind | byte(c.txEpoch&1)*keyPhase
	binary.BigEndian.PutUint32(header[1:], c.counter)
	packet := c.tx.Encrypt(header, packetNonce(c.txEpoch, c.counter), header, body)
	if err := c.sendRaw(packet); err != nil {
		c.fail(err)
		return err
	}
	c.lastSend.Store(time.Now().UnixNano())
	return nil
}

// receive never blocks on application consumers. Reliable chunks are ACKed only
// once retained; a full receive queue leaves the sender to retry with a new nonce.
func (c *Conn) receive(packet []byte) {
	if len(packet) < 21 || len(packet) > maxPacket || c.ctx.Err() != nil {
		return
	}
	body, epoch, advanced, ok := c.decryptPacket(packet, time.Now())
	if !ok {
		return
	}
	c.lastReceive.Store(time.Now().UnixNano())
	if advanced {
		c.confirmKey(epoch)
	}
	switch packet[0] &^ keyPhase {
	case keyUpdate:
		if len(body) != 8 || binary.BigEndian.Uint64(body) != epoch {
			c.fail(errors.New("invalid Noise key update"))
			return
		}
		if !advanced {
			c.confirmKey(epoch)
		}
	case keyACK:
		if len(body) != 8 {
			c.fail(errors.New("invalid Noise key confirmation"))
			return
		}
		c.sendMu.Lock()
		if binary.BigEndian.Uint64(body) == c.txEpoch {
			c.txConfirmed = true
		}
		c.sendMu.Unlock()
	case voice:
		select {
		case c.voices <- body:
		default:
		}
	case control:
		if len(body) < 5 || len(body) > 4+maxChunk {
			c.fail(errors.New("invalid control chunk"))
			return
		}
		seq := binary.BigEndian.Uint32(body[:4])
		if seq == c.expected {
			select {
			case c.recv <- body[4:]:
				c.expected++
			default:
				return
			}
		} else if seq == 0 || seq >= c.expected {
			return
		}
		_ = c.send(ack, body[:4])
	case ack:
		if len(body) == 4 {
			select {
			case c.acks <- binary.BigEndian.Uint32(body):
			default:
			}
		}
	case ping:
		if len(body) == 0 {
			_ = c.send(pong, nil)
		}
	case pong:
	case closing:
		if len(body) == 8 {
			c.fail(&RemoteError{Code: binary.BigEndian.Uint64(body)})
		}
	default:
		c.fail(errors.New("unknown Noise packet type"))
	}
}
func (c *Conn) SendDatagram(data []byte) error { return c.send(voice, data) }
func (c *Conn) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	select {
	case <-c.ctx.Done():
		return nil, c.failure()
	case <-ctx.Done():
		return nil, ctx.Err()
	case b := <-c.voices:
		return b, nil
	}
}

func (c *Conn) deadline(read bool) (time.Time, <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if read {
		return c.rd, c.changed
	}
	return c.wd, c.changed
}
func timerFor(deadline time.Time) (*time.Timer, <-chan time.Time) {
	if deadline.IsZero() {
		return nil, nil
	}
	t := time.NewTimer(time.Until(deadline))
	return t, t.C
}
func (c *Conn) Read(out []byte) (int, error) {
	if len(out) == 0 {
		return 0, nil
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for len(c.pending) == 0 {
		d, changed := c.deadline(true)
		timer, timeout := timerFor(d)
		var err error
		select {
		case c.pending = <-c.recv:
		case <-c.ctx.Done():
			err = c.failure()
		case <-timeout:
			err = os.ErrDeadlineExceeded
		case <-changed:
		}
		if timer != nil {
			timer.Stop()
		}
		if err != nil {
			return 0, err
		}
	}
	n := copy(out, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}
func (c *Conn) Write(data []byte) (int, error) {
	for {
		d, changed := c.deadline(false)
		t, timeout := timerFor(d)
		acquired := false
		var err error
		select {
		case c.writeGate <- struct{}{}:
			acquired = true
		case <-c.ctx.Done():
			err = c.failure()
		case <-timeout:
			err = os.ErrDeadlineExceeded
		case <-changed:
		}
		if t != nil {
			t.Stop()
		}
		if err != nil {
			return 0, err
		}
		if acquired {
			break
		}
	}
	defer func() { <-c.writeGate }()
	total := 0
	for len(data) > 0 {
		if c.writeSeq == ^uint32(0) {
			c.fail(errors.New("control sequence exhausted"))
			return total, c.failure()
		}
		c.writeSeq++
		n := min(len(data), maxChunk)
		body := make([]byte, 4+n)
		binary.BigEndian.PutUint32(body, c.writeSeq)
		copy(body[4:], data[:n])
		if err := c.writeChunk(body, c.writeSeq); err != nil {
			c.fail(err)
			return total, err
		}
		total += n
		data = data[n:]
	}
	return total, nil
}
func (c *Conn) writeChunk(body []byte, seq uint32) error {
	limit := time.Now().Add(8 * time.Second)
	retry := 200 * time.Millisecond
	next := time.Time{}
	for {
		d, changed := c.deadline(false)
		if d.IsZero() || limit.Before(d) {
			d = limit
		}
		if !time.Now().Before(d) {
			return os.ErrDeadlineExceeded
		}
		if !time.Now().Before(next) {
			if err := c.send(control, body); err != nil {
				return err
			}
			next = time.Now().Add(retry)
			retry = min(retry*2, time.Second)
		}
		wake := next
		if d.Before(wake) {
			wake = d
		}
		t := time.NewTimer(time.Until(wake))
		select {
		case id := <-c.acks:
			t.Stop()
			if id == seq {
				return nil
			}
		case <-c.ctx.Done():
			t.Stop()
			return c.failure()
		case <-changed:
			t.Stop()
		case <-t.C:
		}
	}
}
func (c *Conn) setDeadline(r, w *time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r != nil {
		c.rd = *r
	}
	if w != nil {
		c.wd = *w
	}
	close(c.changed)
	c.changed = make(chan struct{})
	return nil
}
func (c *Conn) SetDeadline(t time.Time) error      { return c.setDeadline(&t, &t) }
func (c *Conn) SetReadDeadline(t time.Time) error  { return c.setDeadline(&t, nil) }
func (c *Conn) SetWriteDeadline(t time.Time) error { return c.setDeadline(nil, &t) }

var _ net.Conn = (*Conn)(nil)
var _ io.ReadWriter = (*Conn)(nil)
