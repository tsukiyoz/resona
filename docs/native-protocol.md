# Experimental native wire protocol

2026-09-12: this document describes the QUIC candidate and shared application
messages. The default server now uses [Noise UDP](noise-protocol.md); voice and
CBOR application bodies are shared, but transport framing and trust differ.

QUIC v1, TLS 1.3, ALPN `resona-exp-1`, default UDP port 9988. One client-opened
bidirectional stream carries reliable control; QUIC DATAGRAM carries voice.
This is not HTTP/3. Datagram negotiation is mandatory; 0-RTT commands are disabled.

## Control

Frame: 4-byte big-endian length followed by 1-65536 bytes of CBOR. Envelope:
`[kind, requestID, body]`; body is an embedded value, not a byte string. All structs
are positional arrays in declaration order in `internal/nativewire/wire.go`.
Encoding is canonical; decoding is bounded and rejects tags/indefinite lengths.
Schema changes require a protocol version change. No rolling-upgrade compatibility
is promised for this experimental format.

| Kind | Value | Body |
| --- | --- | --- |
| Hello | 1 | `[nickname, password]`, request 0 |
| Welcome | 2 | initial State, request 0 |
| State | 3 | `[serverName, selfID, ownEpoch, channels, members]` |
| Move | 4 | Command |
| Chat | 5 | Command |
| VoiceState | 6 | Command |
| Reply | 7 | `[code]`, matching request ID |
| Message | 8 | `[channelID, senderID, nickname, text]` |

Channel: `[id, name, description]`. Member:
`[id, channelID, nickname, instance, muted, deafened, epoch]`.
Command: `[channelID, text, muted, deafened]`; unused fields are zero values.
Reply codes: 0 success, 1 rejected, 2 wrong channel, 3 rate limited.
QUIC application close code 2 means authentication failure.

Welcome follows authentication and member registration. Snapshots are pushed on
changes. Move state precedes its success reply. Chat names an explicit target and
is rejected if different from the sender's current channel; no sender echo (local
pending row is confirmed by Reply). Text limit is 8192 UTF-8 bytes; nickname limit
30 code points; password limit 1024 bytes.

Commands serialize per connection with nonreused uint32 request IDs and 8-second
deadlines. Cancellation after starting transmission closes the connection to
avoid continuing with uncertain state; it cannot undo an accepted command.
There is no exactly-once promise. Client session generations isolate old callbacks.

## Voice

All integers are big-endian. Upload header is 7 bytes:
`flags:u8 | senderEpoch:u32 | sequence:u16 | Opus`.
Download header is 13 bytes:
`flags:u8 | recipientEpoch:u32 | sequence:u16 | senderID:u16 | senderEpoch:u32 | Opus`.
These sizes exclude QUIC, TLS, UDP and IP overhead.

Flags: 0 audio, 1 end. Audio payload is 1-1024 bytes; end has no payload. Codec is
fixed Opus mono, 48 kHz, 20 ms. Oversized packets are rejected, never truncated.
The server derives identity/channel from the connection, rejects wrong epochs
or muted senders and forwards only to other non-deafened members of the same
channel. Moving increments the member's epoch. Receivers check both recipient
and sender epochs against current membership, dropping stale channel audio.
Control and datagrams can arrive in different orders; unmatched voice is dropped.

## Bounds and shutdown

Per connection: 16 control messages and four voice packets queued. Control writes
have a 5-second deadline; overflow disconnects slow consumers. Voice overflow
drops new arrivals; locally queued packets older than 100 ms are discarded.
SendDatagram may block internally in quic-go, so dedicated workers call it, with
a 250 ms watchdog that closes stalled connections. These bounds do not guarantee
end-to-end latency or remove already-sent packets.

Per-client token buckets: 10 commands/s (burst 20), 60 voice packets/s (burst 10).
Handshake timeout 5 seconds, idle timeout 30 seconds, keepalive 10 seconds.
Shutdown closes connections and joins workers. Accepted-session caps do not bound
every handshake allocation; public deployment requires load and abuse testing.
