# ADR-0020: Per-recipient voice rings and background Noise rekey

Date: 2026-09-14. Status: Accepted for the native experiment. Updates ADR-0019.

## Relay scheduling

Each server peer owns a mutex-protected MPSC ring with four queued packets and
one send worker. An in-flight packet is owned by that worker, outside ring capacity
and outside the queue lock. Full rings replace the oldest queued voice. Control
messages retain their reliable bounded queues and are never overwritten.

A source loop validates its epoch, mute state and rate under the server state
lock and snapshots eligible recipients and their epochs. It then encodes and
enqueues outside that lock. Membership changes still serialize under the state
lock; stale snapshots remain memory-safe, closed rings reject pushes, and existing
channel epochs let the client discard packets invalidated by a channel move.
There is no goroutine per target per packet and no room worker.

Payload slices are immutable after publication. Pop removes the ring reference;
the worker owns it until SendDatagram returns. Overwrite and Close release queued
references. No pool or cross-recipient mutable buffer sharing is introduced.
Close abandons queued voice and wakes the consumer. Connection cancellation and
the existing 250 ms stalled-send watchdog bound worker exit. The 100 ms freshness
check uses the source's receive time, before decoding, membership lookup or encoding.

The isolated experiments favored mutex rings over the allocating CAS prototype
for CPU/allocation cost; see `test/broadcaster/CAS-RING-EVALUATION.md`. They do not
prove a production latency win. The shared UDP socket write gate remains a separate
contention point. The client capture queue is unchanged in this slice.

## Key updates

Keep `Noise_NK_25519_ChaChaPoly_SHA256` and the pinned flynn/noise v1.1.0.
Use its CipherState.Rekey, reviewed in that version's state.go, with low-level
Cipher for reordered datagrams. No new cipher or key derivation implementation.
The Noise specification describes [Rekey and out-of-order transport](https://noiseprotocol.org/noise.html#rekey);
the phase, confirmation and lifetime rules below are Resona's responsibility.

Each sending direction updates at 12 hours or 2^23 packets. The type byte's high
bit carries generation parity, retaining the 5-byte header and 16-byte tag.
The locally reconstructed AEAD nonce is `(generation << 24) | counter`, so it
continues increasing across key updates; wire counters restart at one under the
new key. Generations start at zero, are confirmed as u64 values inside encrypted
update/ACK packets, and stop at 2^40-2. Neither nonce nor generation may wrap.

The receiver holds current, one previous and one precomputed next cipher. It
promotes only after successful next-key authentication, retaining the previous
generation's separate replay window for up to three seconds of reordering.
Acceptance ends at that deadline; references are dropped on the next received
packet. A forged phase change never advances keys or replay state. Current keys
continue using the normal single-AEAD fast path.

All new-generation packets can establish the update, so losing the initial
announcement does not block audio. The receiver confirms promotion or a repeated
update announcement. The sender retries announcements every five seconds using
fresh counters, continues sending voice/control under the new key, and requires
confirmation before updating again. Independent directional state permits
simultaneous updates. A 30-second confirmation failure or exhausted hard per-key
budget closes the connection; neither downgrading nor indefinite unsafe sending
is permitted. The ordinary 30-second receive-idle limit remains.

Success does not recreate the socket, Conn, stream, reliable sequence, native
member ID, channel, mute state or audio handlers. The public server identity and
stored password remain unchanged. This is symmetric key evolution, not a fresh
DH exchange or post-compromise recovery mechanism. Independent security review,
real long-running WAN impairment and Windows device validation remain pending.

## Compatibility and cleanup

The handshake changes to `RN02` / `resona-noise-exp-2`. Install matching client
core and server builds together. Old experimental peers cannot handshake; there
is no silent fallback. QUIC and TS3 wire formats are unchanged. Existing Noise
identity files/public-key bookmarks remain usable, but old active connections
will end when the old server process is replaced.

The initial, Ant Design and MineRadio HTML prototypes have completed their design
purpose. Their sources, generated pages and npm toolchain are removed; the directory
retains a retirement note and history is preserved in Git. GPUI remains the GUI.
