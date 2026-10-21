# Rust client core

This UI-independent crate starts the migration described in
[ADR-0032](../docs/adr/0032-rust-client-core.md). The shipping desktop still uses
the Go core. There is no second product core selector or alternate transport.

Implemented foundation:

- Existing RN06 cookie / Noise NK handshake, pinned server authentication and
  channel binding; the suite and application protocol are unchanged.
- Header-authenticated datagrams, replay rejection, directional background
  rekey, bounded reliable control, fair voice/control handoff and cancellation.
- Rust Protobuf generated from the same `internal/nativewire/pb/control.proto`,
  frame size/range checks and the existing Ed25519 Hello identity proof.
- Real loopback Go peers, including the actual application server and embedded
  JetStream. The fixture uses temporary test keys and no real user server.

Run `make test-client`, or:

```sh
cargo test --locked --manifest-path client/Cargo.toml
cargo clippy --locked --manifest-path client/Cargo.toml --all-targets -- -D warnings
```

Tests require Go and Rust but no audio hardware, UI, external NATS or CGO.
Protobuf compilation uses a pinned vendored protoc build dependency; no global
protoc installation is needed. Cargo.lock covers all dependencies. CI tests
Linux, Windows and macOS separately.

`Connection` owns its transport worker; keep it alive while using cloned
senders. Await `close()` for joined cleanup; owner drop requests cancellation.
An established connection outlives the dial token. A submitted control write
that is cancelled or dropped closes the transport; a cancelled read retains
partial bytes. Voice send confirms bounded queue admission, not wire delivery.
The caller owns the Tokio runtime; this crate creates no runtime or OS thread.
Native audio callbacks must eventually use a bounded handoff, never run async
network operations directly.

Not migrated yet: product session coordination/reconnect, persistent identity,
bookmarks/credentials/configuration, updates/diagnostics, device/codec/3A/mixer
ownership, GPUI integration and packaging. Do not infer lower memory or game
interference until equivalent release workloads have been measured.
