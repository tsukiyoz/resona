# ADR-0023: Persistent Channels and Owner Claim Renewal

Date: 2026-09-14. Status: Accepted.

## Scope

Extend ADR-0022 with owner-only channel CRUD and explicit offline claim renewal.
Admin delegation, nested channels, channel passwords, bans and ownership recovery
remain separate work. Management stays in GPUI and the native application protocol.

## Claim Renewal

`--reset-owner-claim --access-dir DIR` refuses a valid or corrupt existing owner
record. For an unowned server it atomically replaces the unused claim with a fresh
256-bit random code, printing it once, storing only its digest and 24-hour expiry.
It can replace an expired or still-valid unused code. No server-age deadline.

Normal startup, initialization and renewal hold the same nonblocking OS file lock
for the access directory. Close/process exit releases it; the lock file is never
unlinked. Operators must stop the server, renew, then restart. Older deployed
versions do not know this lock and must also be stopped explicitly. A claim
initialization command never implicitly changes an existing owner.

## Channel State

The CLI creates `access-dir/channels.json` on first startup, seeded from `--channels`
or Lobby/Gaming. Thereafter the persisted record is authoritative. Invalid state
fails startup without overwriting. A versioned record contains the ordered flat
channel list and monotonically increasing next ID. IDs are 1..65535 and are never
reused, even across restarts; simultaneous channels remain limited to 64.

The first channel is the immutable default target (its name/description can be
edited). Delete rejects default or occupied channels. Names are trimmed, nonempty,
at most 100 Unicode code points, without control characters. Description is plain
UTF-8, at most 1024 bytes, NUL forbidden. Combined name/description snapshot budget
is 16 KiB, preserving existing membership frame headroom. Edits use last accepted
write semantics; concurrent edit conflict resolution is not implemented.

A separate channel-management mutex serializes mutations. The shared routing mutex
only checks membership, prepares a copy, and publishes a committed list. Disk I/O
uses a synced temporary file and rename outside the routing lock. While deleting,
Move refuses that ID until persistence finishes, preventing a user from entering
a channel that is about to disappear. Failure clears the reservation and retains
the old state unless rename actually published the new record; a post-rename sync
failure publishes the actual record in memory but reports unconfirmed persistence.
This is a single-active-server design, not a multi-process database.

## Protocol and UI

Kinds 10/11/12 are separate CreateChannel/UpdateChannel/DeleteChannel protobuf
messages. State field 9 is `can_manage_channels`; false by default. The server
checks the signed owner identity regardless of capability visibility. Additive
schema changes retain Noise exp-4 / QUIC exp-3: old servers advertise no management,
old clients ignore the capability. First channel maps to IPC `isDefault`.

GPUI adds a title-bar Plus and channel context menu, bounded edit/confirmation
modals, one outstanding mutation per session and modal-revision isolation. The
client validates again at submission. State events update lists; command replies
never replace full Workspace snapshots. Create does not move the user. Details
refresh or clear on changed/deleted channels. Transport failures are unconfirmed,
not automatically retried. Closing a modal does not retract sent operations.

IPC dispatches owner claim and channel commands in a bounded asynchronous slot,
outside its writer lock. Other state reads, disconnect and shutdown remain usable.
Cancellation propagates from IPC lifetime into the original connection. Tests
cover stalled management versus IPC reads/shutdown and stale client completions.
