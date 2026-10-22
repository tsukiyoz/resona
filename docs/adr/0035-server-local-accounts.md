# ADR-0035: Server-local accounts independent of device keys

Date: 2026-10-22
Status: Accepted; implementation in progress

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

## Existing identities

On the first account-database initialization, reserve existing management UIDs
and their original device UIDs in one SQLite transaction, with a durable import
marker. Account registration by the cryptographically verified original device
may initialize that reserved account exactly once, preserving existing KV role
references. Other devices receive random UIDs. The legacy mapping is consumed
in the same transaction as the first password. Existing web-only management
tokens retain their existing purpose and are not silently account credentials.

No roles are dual-written between SQL and KV. Existing assignments refer to the
reserved account UID; role changes remain KV operations. Preserve and back up
both data stores together. Do not restore one to an unrelated point in time.

## Session behavior

Latest successful voice-account admission replaces any older voice connection
for that UID. Reject invalid/banned admission before replacement. Remove the old
peer's voice/control authority before publishing the new principal, and close it
with a dedicated terminal reason so it does not reconnect and replace the new
device. Web management sessions do not replace voice connections.

Login is an in-session command. Only server-confirmed state denotes success.
Logout revokes the current account credential and closes the connection; a new
connection starts as guest. Password changes revoke old credentials. Network
recovery may reuse a credential only for the same server/key, without replaying
unknown account mutations. Closing a dialog does not undo committed server work.
No account credentials enter logs, diagnostics, ordinary config or snapshots.

## Scope

This iteration adds guest/member/admin/owner presentation, account registration,
password/token login, password changes, logout, owner bootstrap/recovery, and
account-aware management login. It does not add cross-server identity, OAuth,
device pairing, account-wide profile/preferences cloud synchronization, or new
per-channel guest policies. Guest access cannot guarantee account-ban evasion
prevention; a future authenticated-only server/channel policy is separate work.
