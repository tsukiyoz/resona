package noiseudp

import (
	"encoding/binary"
	"errors"
	"time"
)

const (
	rekeyAfter   = 12 * time.Hour
	rekeyPackets = maxCounter / 2
	rekeyRetry   = 5 * time.Second
	rekeyTimeout = 30 * time.Second
	oldKeyGrace  = 3 * time.Second
	// Leave the final 24-bit block unused, including Noise's reserved rekey nonce.
	maxKeyEpoch = (1 << 40) - 2
)

func packetNonce(epoch uint64, counter uint32) uint64 { return epoch<<24 | uint64(counter) }

// Only the sending direction changes here. Confirmation is required before the
// next update, so the receiver never needs to derive an unbounded key history.
// CipherState.Rekey uses Noise's reserved nonce. Wire counters restart under a
// new key, but the reconstructed 64-bit AEAD nonce advances across generations.
func (c *Conn) prepareKeysLocked(now time.Time) error {
	if c.ctx.Err() != nil {
		return c.failure()
	}
	if !c.txConfirmed && now.Sub(c.keySince) >= rekeyTimeout {
		c.fail(errors.New("Noise key confirmation timed out"))
		return c.failure()
	}
	if c.txConfirmed && (c.counter >= rekeyPackets || now.Sub(c.keySince) >= rekeyAfter) {
		if c.txEpoch >= maxKeyEpoch {
			c.fail(errors.New("Noise key generation exhausted"))
			return c.failure()
		}
		c.txState.Rekey()
		c.tx = c.txState.Cipher()
		c.txEpoch++
		c.counter = 0
		c.keySince = now
		c.txConfirmed = false
		c.updateSent = time.Time{}
	}
	if !c.txConfirmed && (c.updateSent.IsZero() || now.Sub(c.updateSent) >= rekeyRetry) {
		var body [8]byte
		binary.BigEndian.PutUint64(body[:], c.txEpoch)
		if err := c.sendLocked(keyUpdate, body[:]); err != nil {
			return err
		}
		c.updateSent = now
	}
	return nil
}

func (c *Conn) refreshKeys(now time.Time) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.prepareKeysLocked(now)
}

func (c *Conn) confirmKey(epoch uint64) {
	var body [8]byte
	binary.BigEndian.PutUint64(body[:], epoch)
	_ = c.send(keyACK, body[:])
}

// The UDP read loop exclusively owns receive keys and replay windows. The old
// and next generations share a phase bit: try the bounded old window first,
// then the one precomputed next key. Forgery cannot advance keys or replay state.
func (c *Conn) decryptPacket(packet []byte, now time.Time) ([]byte, uint64, bool, bool) {
	n := binary.BigEndian.Uint32(packet[1:5])
	if n == 0 || n >= maxCounter {
		return nil, 0, false, false
	}
	if c.rxPrevious != nil && !now.Before(c.previousUntil) {
		c.rxPrevious = nil
		c.previousReplay = replayWindow{}
	}
	phase := packet[0] & keyPhase
	if phase == byte(c.rxEpoch&1)*keyPhase {
		if c.replay.seen(n) {
			return nil, 0, false, false
		}
		body, err := c.rx.Decrypt(nil, packetNonce(c.rxEpoch, n), packet[:5], packet[5:])
		if err != nil {
			return nil, 0, false, false
		}
		c.replay.add(n)
		return body, c.rxEpoch, false, true
	}
	if c.rxPrevious != nil && !c.previousReplay.seen(n) {
		body, err := c.rxPrevious.Decrypt(nil, packetNonce(c.rxEpoch-1, n), packet[:5], packet[5:])
		if err == nil {
			c.previousReplay.add(n)
			return body, c.rxEpoch - 1, false, true
		}
	}
	if c.rxEpoch >= maxKeyEpoch {
		return nil, 0, false, false
	}
	body, err := c.rxNext.Decrypt(nil, packetNonce(c.rxEpoch+1, n), packet[:5], packet[5:])
	if err != nil {
		return nil, 0, false, false
	}
	c.rxPrevious, c.previousReplay = c.rx, c.replay
	c.previousUntil = now.Add(oldKeyGrace)
	c.rx = c.rxNext
	c.rxEpoch++
	c.replay = replayWindow{}
	c.replay.add(n)
	c.rxState.Rekey()
	c.rxNext = c.rxState.Cipher()
	return body, c.rxEpoch, true, true
}
