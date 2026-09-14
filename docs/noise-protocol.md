# Resona Noise UDP experiment

Version/prologue: `resona-noise-exp-5` (native identity and scoped resource watch; matching client/server required). Noise suite:
`Noise_NK_25519_ChaChaPoly_SHA256`. The server relays plaintext Opus between
separately encrypted client/server sessions; this is hop encryption, not E2EE.
All Resona multi-byte header integers are big-endian. Noise primitives use the
library's specified nonce encoding, with `(key generation << 24) | counter`
passed as uint64. The generation is reconstructed locally, not added to each packet.

## Handshake

1. Client sends `RN05 | type=1:u8 | cookie:20 | NK message1:48` (73 bytes).
   Initially cookie is zero. NK payload is empty; no password or audio is sent.
2. Server sends `RN05 | type=2:u8 | cookie:20` (25 bytes), without allocating
   a session or performing DH. Cookie is `timeSlot:u32 | HMAC-SHA256[:16]`,
   bound to protocol, source IP/port, slot and exact NK message. Slots are
   30 seconds; current/previous slots are accepted. Cookie secret is generated
   per server start. It is distinct from the Noise private key.
3. Client repeats message1 with that cookie. Server checks it, connection cap
   and global handshake budget (10/s, burst 20), then performs Noise NK.
4. Server replies `RN05 | type=3:u8 | NK message2:48` (53 bytes). Identical
   message1 retries on an existing endpoint receive the cached identical reply,
   never a reset or second session. An established session is not replaced by
   an unauthenticated new handshake.
5. Client verifies the response against the configured server public key,
   then both sides use directional transport keys. The normal framed Hello
   (nickname/password/Ed25519 public key/signature) and Welcome travel over
   encrypted reliable control. Signature binds to the completed NK handshake
   hash; mandatory verification precedes membership admission. See native-protocol.md.

Client handshake retry is 200 ms with a 5-second/context bound. Application
authentication has a 5-second read deadline after server acceptance. The server
public key must be acquired through a trusted channel; an unknown key is never
learned automatically. Source endpoint changes require reconnect; this version
has no NAT rebinding or migration. Fresh sockets and fresh ephemeral keys are
used on reconnect. Private identity files are never overwritten by initialization.

## Established datagram

```text
key_phase:1 bit + type:7 bits | counter:u32 | ciphertext:N | authentication tag:16
```

The 5-byte header is authenticated associated data. The high bit of the first byte
is the key generation's parity. Each direction starts at generation zero, counter
one. Counters stay below 2^24 and restart only with a new key generation; the full
reconstructed AEAD nonce continues increasing. It is independent of the voice
sequence and channel epochs. All retransmissions use a fresh counter; no frame
is encrypted twice with the same directional key/counter. There is no transmitted
session ID: the UDP source endpoint selects an established session, and its key
authenticates packets. Packets from prior sessions fail authentication.

The 64-packet sliding replay window accepts authenticated reordering, rejects
duplicates/old packets, and is updated only after successful AEAD verification.
Packets over 1200 bytes are discarded. Malformed plaintext is never accepted
merely because it decrypts.

| Type | Value | Decrypted body |
| --- | --- | --- |
| Voice | 4 | existing 7-byte uplink / 13-byte downlink header + Opus |
| Control chunk | 5 | chunk sequence:u32, 1-1000 bytes of control stream |
| Control ACK | 6 | chunk sequence:u32 |
| Ping | 7 | empty |
| Pong | 8 | empty |
| Close | 9 | application error code:u64 |
| Key update | 10 | sending key generation:u64 |
| Key confirmation | 11 | acknowledged peer key generation:u64 |

Voice costs 28/34 bytes above Opus excluding UDP/IP. It has no ACK, retransmission,
FEC, padding, or automatic congestion estimator at this layer. Existing application
channel epochs, per-sender rate limit and stale local packet dropping still apply.

## Reliable control and lifecycle

Each direction has one outstanding chunk. ACK means the receiver retained the
chunk, not that the command succeeded; normal application Reply still confirms
commands. Expected chunk increments only after enqueueing into the 32-chunk
receive buffer. Full buffers leave chunks unacknowledged. Old chunk retries are
ACKed without duplicate delivery. Future chunks are not retained. Retry delay
starts at 200 ms and doubles to 1 second, with an 8-second per-chunk ceiling and
the caller's earlier write deadline. Timeouts close the session, because partial
stream writes cannot be safely retried as new commands.

Voice receive queue has four packets; overflow drops arrivals. Socket writes
and shared server-writer acquisition are bounded to 250 ms each. Authenticated
traffic refreshes idle time. A 5-second maintenance check sends keepalive after
10 seconds without sending, and expires peers after 30 seconds without receiving.
The connection has no fixed 24-hour expiry. Directional keys update in the
background at 12 hours or 2^23 sent packets using the library's Noise Rekey.
Voice and control continue on the same session while confirmation is pending.
Update announcements retry every five seconds with fresh counters; the receiver
can also discover and confirm a new key from any successfully authenticated packet
of that generation. A generation cannot update again before confirmation.

The receiver keeps separate replay windows for current and previous keys; the
previous key is accepted for at most three seconds after promotion. Exactly one
next key is precomputed. An opposite phase is tried against eligible previous
packets and then that next key; authentication must succeed before promotion.
Unknown generations are not searched by repeated key derivation. Delayed old
confirmations cannot confirm a later sending generation.

Unconfirmed updates fail closed after 30 seconds. The hard limit remains
2^24-1 packets per key, and generation may not advance beyond 2^40-2, keeping the
64-bit nonce below Noise's reserved values. Normal update success does not reset
the control stream, user identity, channel, mute settings, or socket. This does
not rotate the server's static public key or add fresh DH entropy.

Server voice egress uses a four-slot per-recipient mutex MPSC ring, plus at most
one packet in the send worker. Overflow replaces the oldest queued voice. Packets
older than 100 ms since source receipt are discarded before sending. Control is
not subject to this overwrite policy; the client capture queue is unchanged.

Close packets are authenticated best effort. Listener shutdown attempts them
before closing UDP, with a 1-second socket-close watchdog, then joins maintenance
workers. Lost Close packets are handled by peer idle expiration. Local cancel
unblocks readers/writers; established connections outlive the dial context.

Further work: adaptive voice congestion response,
selective-repeat control if measured necessary, endpoint migration, independent
security review, WAN impairment and capacity measurements. Do not infer these
from successful loopback tests.
