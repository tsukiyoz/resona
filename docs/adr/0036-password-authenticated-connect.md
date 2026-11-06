# ADR-0036: Password-authenticated server discovery

Date: 2026-11-05. Status: Accepted, experimental RN07; paired upgrade required.

## Decision

Keep Noise UDP as the only transport. Add RFC 9807 OPAQUE authentication over
its provisional encrypted reliable stream before Hello or account credentials.
Users of password-protected servers need an address and a shared connection
password. Key discovery is explicitly untrusted; OPAQUE binds authentication to
the exact Noise handshake hash. An optional supplied Noise pin is always enforced.
Passwordless servers require a trusted invite pin, not first-use automatic trust.
Hello field 2 is reserved; no shared plaintext password crosses the connection.

Use Ristretto255 OPRF/AKE, TripleDH/SHA-512, Identity KSF, fixed role identities
and a versioned context (noise-protocol.md). The server creates one immutable
OPAQUE registration locally at password setup or rotation, not a network
registration endpoint. A per-attempt OPAQUE object owns ephemeral state. Password
plaintext is only needed during explicit setup or rotation. Identity KSF avoids adding
memory-hard work to every reconnect, but does not harden a stolen registration
against password guessing. Use random shared secrets. Account passwords retain
Argon2id. Anyone knowing the shared password can impersonate an unpinned server;
use a trusted pin when independent server identity is needed.

## Evaluated Dependencies And Evidence

- Go: `github.com/bytemare/opaque v0.18.0`, evaluated commit
  `b85ab30b461163e64aa0f50dbfb6f744454c783e`. Uses the actual Configuration,
  local registration, GenerateKE1/2/3 and LoginFinish APIs. This implementation
  has no independent audit claim.
- Rust: `opaque-ke =4.0.1`, evaluated commit
  `75fe4cdddb7946440054da0c8e7cdd73828af3f9`. Uses actual ClientLogin
  start/finish with identical suite, identifiers and channel context. Its older
  NCC review does not constitute an audit of the currently integrated 4.x code.
- Real Rust/Go UDP interoperability covers correct/wrong passwords, public
  pin requirements, strict wrong-pin rejection, account restoration, replay,
  mismatched channel binding and cancellation before KE2. Go race tests share
  the immutable server registration across concurrent authentication attempts.
- A local Apple M2 Go client+server net.Pipe benchmark measured approximately
  0.75–0.78 ms/handshake, 48.9 KiB and 565 allocations. This excludes startup
  registration and WAN delay, and is not a Rust/client or production capacity
  claim. No established voice packet fields, timers or DSP changes.

Existing connection caps, handshake rate budget and bounded setup deadlines
limit admitted authentication work. No new idle worker or authentication queue.
Discovery responses are smaller than requests. Discovery, cookies and provisional
Noise completion cannot admit a member without the application authentication.

## Persistence And Lifecycle

Discovered public keys remain attempt-local. Persisting them into the bookmark
would change the existing credential key and strand saved account/server secrets.
Connect-time remembering saves only after a successful current connection intent.
Explicit Add/Edit bookmark saves may also replace the password in OS credential
storage without connecting. The blank masked field preserves an unchanged
destination's password; opening the form never reads it. The regular form no
longer edits pins, but preserves existing pins and their strict validation.
Credential and bookmark stores are not a transaction: failed new-bookmark saves
remove the new credential, while failed existing-bookmark saves report an already
updated password explicitly. Cancellation
closes provisional transports; reconnect repeats PAKE before restoring identity.

The private RESONA_AUTH KV is startup authority: FileStorage, History=1, no TTL.
One versioned record holds the OPAQUE registration, server keys/OPRF seed and an
independent random edit revision. No plaintext password is persisted. A durable
initialization marker in management state prevents missing/deleted authentication
from being recreated as public access. Missing, malformed or expiring established
records fail closed. Owner-only HTTP change/reset serializes writers, compares
the revision and commits KV CAS before publishing the authentication object.
KV IO and OPAQUE setup do not hold the voice routing lock. Admission checks the
same object under that lock, rejecting authentication completed before rotation
but not yet admitted. Established voice sessions are unchanged. Partial storage
publication or lost responses report unknown results rather than automatic retry.
Readback reconciles uncertain writes; if authoritative state is unavailable,
gate new admissions until storage is restored and the service restarts, without
closing established sessions. Backups and one-time reset responses are private,
never regular KV resources or diagnostic payloads. Browser close/logout/navigation
clears one-time content. Lost random reset output requires an explicit new reset;
neither KV nor logs can recover plaintext. Offline password set/reset shares the
access lock, reads supplied secrets from stdin and displays generated secrets
only after a confirmed commit. Old password file/environment inputs are removed.

Owner claims remain digest + fixed 24-hour expiry in the resource CAS record,
so assigning the owner and consuming the claim stay atomic. Expiry is enforced
on use; it does not depend on physical TTL cleanup. Explicit CLI output is once
only after persistence, with no default plaintext file or operational secret log.

Independent protocol review, active network adversary testing, Windows real-user
acceptance and production capacity testing remain separate verification work.

References: [RFC 9807](https://www.rfc-editor.org/rfc/rfc9807),
[Go source](https://github.com/bytemare/opaque/tree/b85ab30b461163e64aa0f50dbfb6f744454c783e),
[Rust source](https://github.com/facebook/opaque-ke/tree/75fe4cdddb7946440054da0c8e7cdd73828af3f9).
