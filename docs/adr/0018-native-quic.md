# ADR-0018: Native QUIC server and client adapter

Date: 2026-09-11. Status: Accepted for experimentation; production rollout pending.

The user authorized an independent native server now, retaining selectable TS3
client compatibility during migration. This updates the earlier deferral of a
second protocol; it does not authorize removing TS3.

`cmd/resona-server` is a Go relay with no client, TS3, GUI or codec dependency.
`internal/nativewire` defines the wire; `internal/server` owns membership and
forwarding; `internal/protocol/native` implements existing client/audio contracts.
The protocol router selects exactly one adapter from the bookmark. GPUI continues
to exchange control/state with Go core through existing IPC, not per-frame audio.

One QUIC stream carries bounded CBOR array control messages; QUIC DATAGRAM carries
fixed headers and raw Opus. CBOR is provisional: compact positional messages and
no schema generator suit matching-build experiments, but it is not inherently
better than Protobuf. Before a public stable protocol compare schema evolution,
tooling, size and parsing costs. Voice packets need not use either format.

TLS system trust is default. Explicit SHA-256 leaf certificate pins replace
issuer/hostname trust but retain validity checks and proof of private-key
possession. No silent trust acceptance or fallback. Credentials bind bookmark,
protocol, address and pin while preserving legacy TS3 keys. Guests have transient
identities and optional shared-password access, not accounts or permissions.

Keep gopus in the Go client. Server relaying requires no Opus computation and
does not justify a Rust codec subprocess. Future client evaluation should compare
gopus, libopus, restsend/opus-rs and Remade-With-Rust/rusty-opus at equal settings
on Windows x64/macOS ARM64: encode, multi-speaker decode, frame-time tails, memory,
PLC/FEC and quality. Upstream benchmark claims are not Resona measurements. FFI
or independent Rust audio-engine migration requires measured benefit; audio never
belongs on the GPUI UI thread.

## Evaluated dependencies

| Module | Version / evaluated tag commit | License |
| --- | --- | --- |
| quic-go | v0.62.0 / 738877626f361538cf07bc4c40cef79483a3ddbf | MIT |
| fxamacker/cbor/v2 | v2.9.3 / f0c90b6d3bc7b52873bd26aff2c3891fd0f203ca | MIT |

Exact module content is pinned by go.sum. Real loopback integration covers TLS,
control, datagrams, bad trust/password, channel isolation, Opus decode and shutdown.
quic-go's queue implementation was inspected: SendDatagram can block, hence the
bounded application queues, dedicated send workers and stall watchdog.

This slice has static channels, at most 64 accepted clients and bounded/rate-limited
queues. It establishes no capacity target or public-internet security readiness.
Windows UI/audio, WAN impairment, long-running loads, native member notification
sounds and persistent identity/permissions remain separate acceptance work.

See [server operation](../server.md) and [wire specification](../native-protocol.md).
