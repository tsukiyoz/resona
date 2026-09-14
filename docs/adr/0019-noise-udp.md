# ADR-0019: Noise UDP as the primary native experiment

Date: 2026-09-12. Status: Accepted for experimentation, not a production security
or performance certification. Updates ADR-0018: QUIC remains an explicit baseline,
but new server runs default to Noise UDP. TS3 compatibility is not a constraint
on the native protocol; the existing TS3 client adapter remains selectable.

## Decision

Use `Noise_NK_25519_ChaChaPoly_SHA256` via `github.com/flynn/noise v1.1.0`
(BSD-3-Clause), pinned by go.mod/go.sum. The evaluated source is that module
version, especially HandshakeState, CipherState.Cipher, NK pattern and vectors.
Retrieval of the upstream tag commit failed during this change; no commit hash is
claimed. The dependency's handshake and crypto primitives are reused, not a new
implementation of DH or AEAD. No claim of an independent security audit.

NK requires the server's X25519 public key in the bookmark. A fresh client
ephemeral key and server ephemeral key produce separate directional transport
keys. The application password is sent only after the final handshake response
has been verified. NK authenticates the server; guest/shared-password admission
is still application behavior, not persistent client identity.

`internal/noiseudp` owns handshake retry/cookies, authenticated packet counters,
replay detection, bounded control delivery and UDP lifecycle. `nativewire` exposes
the minimal stream/datagram connection interface also implemented by QUIC.
Server membership/chat/relay and client audio contracts are shared.

The raw public key is NOT a TLS certificate fingerprint. `serverPublicKey` is a
separate required 64-hex field for `resona-noise`; `resona` preserves QUIC and its
optional `certificateFingerprint`. There is no trust discovery or fallback.
Changing public key, protocol or destination invalidates saved credentials.

## Tradeoffs

Voice has no transport ACK/retransmission. The outer header plus authentication
tag costs 21 bytes, excluding the existing 7/13-byte voice header and IP/UDP.
This does not establish a CPU, bandwidth or latency win over QUIC.

Control uses a bounded stop-and-wait chunk protocol: simple and intentionally
conservative, but one chunk per RTT limits large snapshots over WAN. It is not
a general-purpose high-throughput transport. Retransmission uses fresh encryption
counters, so ACK loss can retry without violating replay protection. Voice does
not wait for a control ACK. Receiver retention is bounded; no selective-repeat
window or automatic bandwidth estimator is claimed.

Noise does not supply UDP connection management. Those surrounding mechanisms
are Resona experimental code and need further review/load/impairment testing.
Cookie validation before DH bounds spoofed allocation and reflection size, but
does not establish DDoS resistance. Current voice rate limits are not congestion
control; adaptive sender rate and loss feedback remain required before broad WAN
deployment. The initial experiment expired at 2^24-1 sent packets or 24 hours.
ADR-0020 supersedes that lifetime policy with confirmed background key updates,
retaining hard nonce limits and a bounded failure path.

See [Noise wire specification](../noise-protocol.md) and [server operation](../server.md).
