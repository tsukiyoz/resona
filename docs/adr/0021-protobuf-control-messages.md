# ADR-0021: Protobuf control messages

Date: 2026-09-14. Status: Accepted for the native experiment. Supersedes the
provisional CBOR selection in ADR-0018 for control messages only.

## Decision

Use proto3 for Hello, State, Command, Reply, Message and the control envelope.
The schema is `internal/nativewire/pb/control.proto`. Keep the four-byte big-endian
frame length, 64 KiB bound, command request IDs and existing reliable transport.
Voice retains its fixed 7/13-byte header and raw Opus payload. No gRPC, HTTP/2 or
new audio/IPC dependency is introduced.

Generated types remain private to the wire adapter. Server/client state remains
copyable Go values, without protobuf runtime state. Conversion validates uint16
IDs and uint8 reply codes before narrowing. Snapshot repeated entries are counted
with protowire before decoding allocates nested objects, preserving 64-member and
64-channel limits. Group fields in State are rejected. Envelope/body decoding has
a recursion limit of eight; existing nickname, password, text, authentication and
channel checks remain application responsibilities.

Unknown fields in known messages are discarded. Missing scalar fields have proto3
defaults; VoiceState sets both flags and is not a patch operation. Added fields
must have safe defaults, removed numbers/names must be reserved, and future partial
updates require explicit presence. Unknown commands do not acquire compatible
semantics merely because protobuf can parse them.

## Evaluated dependency and generation

`google.golang.org/protobuf v1.36.12`, BSD-3-Clause, release commit
[`cdd4c5f7406e82462949c7a65defa9f3029c162d`](https://github.com/protocolbuffers/protobuf-go/commit/cdd4c5f7406e82462949c7a65defa9f3029c162d).
Module contents are pinned by go.sum. The actual generated messages, runtime
encode/decode, bounds, unknown fields, native QUIC/Noise integration and race
checks were exercised, rather than relying on release claims.

`go generate ./internal/nativewire/pb` (also `make generate`) checks protoc 29.3,
builds the matching protoc-gen-go from the pinned module in a temporary directory,
and regenerates checked-in Go code. Repeating generation produced the same SHA-256
`623fc899afada2b75b751ad62aec3bc4fc1f2154fdf6bfbbc9585d6886b2fc02`.
Ordinary builds do not require protoc. See the pb directory README.

## Performance and reflection

CBOR defines typed binary values, not a reflection requirement. The old
fxamacker/cbor v2.9.3 implementation uses reflect and caches type/field metadata
and encoder functions in cache.go. The positional-array encoding avoids field
names and can be smaller than tagged protobuf messages. The standard Go protobuf
runtime also uses reflection/metadata when initializing message information and
uses cached codec methods; generated types are not a claim of zero reflection.

The independent module `test/controlcodec` freezes the old CBOR control codec and
compares complete framing and application-value conversion. CBOR is removed from
product dependencies. Measurements show faster decoding on these fixtures, not
universally smaller packets or fewer allocations. Contiguous temporary generated
member/channel arrays reduce the 64-member protobuf encoder from 74 allocations
to eight without publishing generated objects into application state. See its
README for exact measurements and limitations. No codec pools or unsafe buffers.

## Migration and verification

QUIC ALPN becomes `resona-exp-2`; Noise handshake becomes `RN03` /
`resona-noise-exp-3`. Matching client core and server are required; CBOR peers
cannot negotiate this protocol and there is no fallback. Existing identity keys
and bookmarks remain valid. Background key updates from ADR-0020 are unchanged.

Core and command-package regression tests, race tests for nativewire/noiseudp/
native adapter/server, byte-level golden fixtures, range/type/default/unknown-field
checks and bounded fuzzing pass. Local core and Windows server builds pass.
No remote deployment, Windows device acceptance or overall server performance
improvement is claimed by this serialization comparison.
