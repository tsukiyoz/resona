# ADR-0035: Server-local accounts independent of device keys

Date: 2026-10-22
Status: Accepted

## Decision

Connecting requires an authenticated device key but not an account. Guests can
use the current voice/chat workflow. Optional account login grants a stable UID
scoped to this server. Password and revocable account-token authentication share
that UID; device UIDs remain separate and are never trusted as account claims.
Casbin assigns member/admin/owner to account UIDs, not nicknames or devices.

Use SQLite for accounts, Argon2id PHC password hashes with random per-password
salts, and account-token digests. Keep channels, server settings and existing
role/ban assignments in the management KV; account credentials are never resource
projections. No central login service or third-party provider is implemented.
The server owns all account writes; no direct database or NATS client access.

The driver is modernc.org/sqlite v1.61.0, selected for the existing CGO-disabled
server build. Integration evidence must include actual reopen, transaction,
constraint and server build tests, not package claims. Database schema version 1
uses foreign keys, unique normalized names and token digests, WAL and FULL sync.
Password work is limited to two concurrent hashes and never uses routing locks.

## Fresh account identities

At the maintainer's request, no device identity adopts an account or privileges.
This experimental upgrade clears previous device-bound owner/admin credentials,
claims and bans once. All devices enter as guests; users explicitly register in
Settings -> Identity. Every account gets an independent random UID. New owner claims
can only be consumed by a logged-in account. Existing web-only management tokens
are not account login credentials. Back up both SQLite and KV together.

Account nicknames are server-authoritative and synchronize on login/profile
updates; guest nicknames remain connection-local. Management identifies accounts
by UID plus nickname. No preference cloud synchronization is implied.
Registration uses the requested nickname or the current guest nickname when
omitted. In-session admission advances the voice epoch to isolate old frames,
while preserving the channel and transmit/listen intent. Device selection, gains,
activation and processing preferences remain local and are neither uploaded nor
replaced with defaults on admission.

## Session behavior

Latest successful voice-account admission replaces any older voice connection
for that UID. Reject invalid/banned admission before replacement. Remove the old
peer's voice/control authority before publishing the new principal, and close it
with a dedicated terminal reason so it does not reconnect and replace the new
device. Web management sessions do not replace voice connections.

The account has one native recovery token at a time, rotated transactionally on
each successful native authentication. Rotation invalidates even disconnected
devices, not just peers currently in the routing map. An exported token is
therefore rotated after native login too. Browser password sessions validate the
account password version independently and do not mint a native token.

Login is an in-session command. Only server-confirmed state denotes success.
Logout revokes the current account credential and closes the connection; a new
connection starts as guest. Password changes revoke old credentials. Network
recovery may reuse a credential only for the same server/key, without replaying
unknown account mutations. Closing a dialog does not undo committed server work.
No account credentials enter logs, diagnostics, ordinary config or snapshots.

### Local login persistence (2026-10-23)

Retain the existing rotating native recovery token across ordinary disconnect
and application restart in the OS secure store (macOS Keychain / Windows
Credential Manager). Use a distinct service/key namespace from server passwords,
bound to the same bookmark, destination and pinned server key. The existing
30-day lifetime renews on successful native authentication; a second access/
refresh-token protocol or idle refresh timer is unnecessary for this scope.

Persist at the confirmed account reply, including network recovery before any
subsequent channel restoration can fail. A bounded credential worker serializes
operations independently of the transport/audio owner. Awaiting a credential
receipt never prevents the session from processing another command. Explicit
logout, terminal account replacement/revocation, bookmark deletion and target
changes clear the corresponding stored credential. Invalid saved credentials
fail visibly; the next deliberate connection may enter as guest for manual
login. Denied reads fail explicitly; denied saves preserve the admitted account
with a persistence warning. No plaintext fallback is permitted.

Shutdown waits at most 250ms for queued credential work; unanswered OS prompts
must not hold the application open. Persistence cannot be guaranteed if the user
exits while OS authorization is pending or denies that authorization. Native
secure storage remains unavailable on unsupported platforms.

## Scope

This iteration adds guest/member/admin/owner presentation, account registration,
password/token login, password changes, logout, owner bootstrap/recovery, and
account-aware management login. It does not add cross-server identity, OAuth,
device pairing, account-wide profile/preferences cloud synchronization, or new
per-channel guest policies. Guest access cannot guarantee account-ban evasion
prevention; a future authenticated-only server/channel policy is separate work.
