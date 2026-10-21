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
- Authenticated sessions, bounded commands/chat, scoped resource observation,
  serialized connect/disconnect and cancellable recovery with muted restoration.
- Compatible identity seeds/bookmarks and strict migration/write protection;
  macOS Keychain and Windows Credential Manager preserve existing account keys.
  Blocking credential operations belong on a non-UI worker.

Run `make test-client`, or:

```sh
cargo test --locked --manifest-path client/Cargo.toml
cargo clippy --locked --manifest-path client/Cargo.toml --all-targets -- -D warnings
```

Tests require Go and Rust but no audio hardware, UI, external NATS or CGO.
Protobuf compilation uses a pinned vendored protoc build dependency; no global
protoc installation is needed. Cargo.lock covers all dependencies. CI tests
Linux, Windows and macOS separately.

`--features audio` additionally builds the miniaudio device adapter, vendored
SpeexDSP and official libopus (1.6.1+, found through pkg-config; `OPUS_STATIC=1`
selects static linking). It does not open devices in the default tests. Set
`RESONA_TEST_NATIVE_AUDIO=1` and `RESONA_WEBRTC_PLUGIN_DIR` to require tests of
the existing product AEC3/NS/AGC2/RNNoise library. CI builds evaluated libopus
commit 22244de5a79bd1d6d623c32e72bf1954b56235be for each platform.

`Connection` owns its transport worker; keep it alive while using cloned
senders. Await `close()` for joined cleanup; owner drop requests cancellation.
An established connection outlives the dial token. A submitted control write
that is cancelled or dropped closes the transport; a cancelled read retains
partial bytes. Voice send confirms bounded queue admission, not wire delivery.
The caller owns the Tokio runtime. Native audio callbacks use bounded SPSC
rings and never run codecs, DSP or network operations. Devices must be opened
on their owning audio worker because the callback wakes that thread.

Not migrated yet: product projections, updates/diagnostics, device/codec/3A/mixer
ownership, GPUI integration and packaging. Do not infer lower memory or game
interference until equivalent release workloads have been measured.
