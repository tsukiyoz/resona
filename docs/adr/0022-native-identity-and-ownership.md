# ADR-0022: Native Identity and First Owner

Status: Accepted for the first implementation slice; GUI device acceptance and
security review are separate from automated correctness checks.
Date: 2026-09-14.

Follow-up: ADR-0023 adds persistent channel CRUD and explicit claim renewal.
Scope exclusions below describe the original identity slice.

## Direction

The user confirmed that Resona will ultimately use its own client/server
protocol, retiring TS3 compatibility rather than replicating the TS3 permission
system. This supersedes earlier roadmap/AGENTS wording about a future compatible
TS3 server. During transition, client features follow protocol capabilities and
server-authorized actions. Management belongs in the native client first; no HTTP
listener or second product frontend is introduced.

Sequence: persistent identity and first owner, simple roles, persistent dynamic
channels, then the full client management page. This slice implements the first
step and a small identity/ownership panel, not administrator delegation, bans,
channel CRUD or owner transfer. No shared administrator password is introduced.

## Authentication

- Product native connectors load a separate 32-byte Ed25519 seed from
  `os.UserConfigDir()/resona/native-identity.key`. A corrupt file is preserved
  and causes connection failure. Complete synced files are published using a
  no-replace hard link. File mode is 0600; Windows also relies on the user's
  profile-directory ACL. TS3 identity files are not imported or modified.
- The persistent ID is lowercase hex SHA-256 of the Ed25519 public key.
  Session member IDs remain short uint16 values for voice routing. The full
  persistent ID is sent only in the recipient's own State in this slice.
- Hello adds public_key (32 bytes) and signature (64 bytes). The proof is
  Ed25519 over SHA-256(domain || binding || Pack(unsigned Hello)), where domain
  is `resona-client-identity-v1` followed by NUL. Pack includes the full frame
  and an empty signature. Nickname and server password are bound as well.
- Noise binding is the completed NK handshake hash from flynn/noise's existing
  `ChannelBinding()` API. It is immutable across background rekeys. QUIC uses
  TLS ExportKeyingMaterial with label `EXPORTER-Resona-Identity-v1`, nil context,
  and 32-byte output. This binds the proof to the authenticated transport,
  avoiding replay or relay through a different transport session.
- No new crypto library or handshake pattern. Mandatory identity changes the
  protocol to Noise exp-4 / RN04 and QUIC ALPN resona-exp-3; no anonymous fallback.
- A zero-value `native.Connector` intentionally creates ephemeral test/bot
  identities. Product core always constructs `native.NewDefault()`.

## Owner Persistence

`--access-dir` defaults to the server config directory's `access` subdirectory.
An offline `--init-owner` invocation generates a 256-bit random token, prints it
once to the invoking operator, and writes only SHA-256(token) plus 24-hour expiry
to `claim.json`. Normal startup never prints it. Existing claim files are not
overwritten; missing provisioning disables claim without stopping guest use.

ClaimOwner is distinct kind 9 with its own protobuf body and nonzero request ID.
It requires authenticated identity, correct unexpired token, and no existing
owner. The server checks it regardless of what controls the GUI shows. Native
command rate limits also cover attempts. Durable `owner.json` contains version
and owner ID. Atomic no-replace publication makes concurrent claims single-winner,
including competing processes. Persistence precedes success and state broadcast;
disk failure returns failure/unknown rather than granting an in-memory owner.
Owner record presence makes the old claim unusable even if claim.json remains.

The server is intended to have one active process per access directory; this is
not a multi-process live cache-coherence protocol. A read-only key mount may be
retained, but access-dir must be writable for claim. Back up both the server
ownership record and the client's identity securely. Losing the client key loses
proof of ownership. Recovery/transfer is not implemented and must not silently
create a new owner. Expired-code renewal is an explicit offline operation after
checking that no owner exists, not automatic reset on launch.

## Client Flow

State/IPC expose serverRole and canClaimOwner alongside existing identityUID.
Current roles are member and owner. TS3 leaves new fields empty/false. The GPUI
panel shows only supported identity/claim actions and captures session+modal
revision. Token input is masked, never persisted, and cleared on submit, close,
disconnect or target change. Duplicate submission is blocked. Closing dismisses
the panel; it does not promise to undo an operation already sent. On timeout,
authoritative later owner state wins; reconnect recovers persisted state.

Service operations capture the original connection, check the session before
dispatch and before returning, and never apply a stale result to another server.
The server state broadcast precedes the successful command reply. No account
platform or external login provider is required; those can later bind to identities.
