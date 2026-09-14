# Experimental native wire protocol

2026-09-14: CreateChannel field 3, UpdateChannel/Channel field 4 carry `bitrate`
in bits/second; State field 10 is `can_configure_channel_audio`. Positive values
are 16000..64000 in steps of 1000. Zero creates at 32000, preserves the old value
on update, and means legacy 48000 in snapshots/stores. Voice framing and protocol
versions are unchanged. This is a client encoder target, not congestion control
or a server-enforced limit; older clients continue encoding at their original
rate. See [ADR-0024](adr/0024-channel-audio-quality.md).

2026-09-14: this document describes the QUIC candidate and shared application
messages. The default server now uses [Noise UDP](noise-protocol.md); voice and
Protobuf application bodies are shared, but transport framing and trust differ.

QUIC v1, TLS 1.3, ALPN `resona-exp-3`, default UDP port 9988. One client-opened
bidirectional stream carries reliable control; QUIC DATAGRAM carries voice.
This is not HTTP/3. Datagram negotiation is mandatory; 0-RTT commands are disabled.

## Control

Identity is mandatory: Hello includes Ed25519 public key and a signature bound
to this encrypted transport session. State includes the recipient's identityUID,
serverRole and canClaimOwner. ClaimOwner kind 9 has its own `{token}` protobuf
body and a nonzero request ID. Server-side identity verification and ownership
checks are authoritative; see ADR-0022 for exact proof and persistence rules.

Frame: 4-byte big-endian length followed by 1-65536 bytes of Protobuf Frame.
Schema: `internal/nativewire/pb/control.proto`. Frame fields are kind (enum, 1),
request (uint32, 2), body (bytes, 3). Body contains the selected protobuf message;
it has no additional frame prefix. Empty bodies are valid for default-valued
messages such as a successful Reply. Application Go structs are mapped explicitly
at the wire boundary and are not the schema.

Decoding bounds recursion, validates numeric ranges before narrowing, and limits
snapshot members/channels before allocating repeated message objects. Unknown
fields in known messages are discarded; deprecated group fields in State are
rejected. New fields require safe defaults; field numbers cannot be reused.
Schema compatibility does not imply compatibility of new commands/semantics.
The CBOR-to-Protobuf change requires matching client/server versions; no fallback.

| Kind | Value | Body |
| --- | --- | --- |
| Hello | 1 | Hello, request 0 |
| Welcome | 2 | initial State, request 0 |
| State | 3 | State |
| Move | 4 | Command |
| Chat | 5 | Command |
| VoiceState | 6 | Command |
| Reply | 7 | Reply, matching request ID |
| Message | 8 | Message |
| ClaimOwner | 9 | ClaimOwner {token} |
| CreateChannel | 10 | CreateChannel {name, description} |
| UpdateChannel | 11 | UpdateChannel {id, name, description} |
| DeleteChannel | 12 | DeleteChannel {id} |

Channel, Member and Command fields are defined in the schema. Unused command
fields use proto3 defaults. VoiceState sets both flags, not a partial patch.
Reply codes: 0 success, 1 rejected, 2 wrong channel, 3 rate limited.
Channel management additionally uses 4 permission denied, 5 channel not empty,
6 default channel, 7 persistence failed/unconfirmed, 8 channel count/ID exhausted.
State field 9 (`can_manage_channels`) defaults false; only owners on a configured
persistent server receive true. Commands 10-12 require server-side owner checks.
The first channel in the ordered state is the default channel. These additive
changes retain the current transport versions; no voice header fields change.
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
replaces the oldest queued server voice; the client capture queue drops arrivals.
Locally queued packets older than 100 ms are discarded.
SendDatagram may block internally in quic-go, so dedicated workers call it, with
a 250 ms watchdog that closes stalled connections. These bounds do not guarantee
end-to-end latency or remove already-sent packets.

Per-client token buckets: 10 commands/s (burst 20), 60 voice packets/s (burst 10).
Handshake timeout 5 seconds, idle timeout 30 seconds, keepalive 10 seconds.
Shutdown closes connections and joins workers. Accepted-session caps do not bound
every handshake allocation; public deployment requires load and abuse testing.
