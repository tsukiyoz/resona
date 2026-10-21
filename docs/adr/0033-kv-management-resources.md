# ADR-0033: Durable Management Resources in NATS KV

Date: 2026-10-21

## Decision

Reuse the single in-process JetStream broker for chat and management resources.
`RESONA_RESOURCES/state` contains a versioned document with channels, the next
channel ID, immutable owner identity and unused claim digest/expiry. Use file
storage, history 1, a 128 KiB value limit and a 2 MiB logical bucket limit. There
is no TTL: claims expire, channels and ownership do not. The value budget covers
JSON escaping of the existing 16 KiB string budget. Server private keys and raw
claim codes do not enter KV. Listen addresses and secrets remain startup config.

One aggregate document is intentional at the current 64-channel limit: owner
creation/claim consumption and channel creation/ID allocation must commit together.
NATS KV CAS protects concurrent writers; three conflicts within a two-second
operation fail for explicit retry. Splitting keys later requires a transaction
design, not independent writes to owner/claim. Local mutations serialize outside
the routing mutex. Authorization reads a short memory lock never held during RPC
or disk I/O. Voice queues and membership remain transient and outside KV.

Channel ordering extension: channel records now use version 2, keeping array
order and an explicit immutable default_id separately. Version 1 is normalized
on read using its original first channel; the next successful CAS writes v2.
Missing/unknown default IDs in v2 are invalid. Reordering moves an existing ID
before another ID (zero appends), preserving ID allocation, default identity,
metadata and live memberships. The authenticated owner uses the same bounded
mutation/CAS and interest projection path. This adds a Protobuf command and an
explicit can_reorder_channels capability; no extra watcher or timer is created.

One Serve-owned KV watcher receives initial state and future revisions on the
same consumer. Valid revisions feed existing scoped snapshots and per-connection
revisions. State remains queued before the WatchResources reply. Scope comparison
suppresses unchanged views; no per-user KV consumers, configuration polling or
changes to the existing List+Watch protocol. Watch failure, deletion, invalid schemas or illegal
transitions stop the service rather than serving stale permissions. Cancellation
joins observation and peer workers before broker shutdown.

Future Web/admin transports must use shared authorized mutation methods, including
occupied/default-channel validation, instead of raw KV writes. Web management,
arbitrary selectors, delta events, clusters and additional roles remain deferred.

## Migration and Operations

Under the exclusive access lock, a new bucket imports legacy JSON once. Channel
IDs/allocator, bitrate, owner and unconsumed claim expiry are preserved. Malformed
legacy input fails before bucket creation; originals remain untouched. Existing
buckets with missing/corrupt/future state never reimport old files. After migration,
old JSON has no effect. Stop and back up the whole data directory and private key
before upgrading; rollback needs the matching backup because JSON stops tracking
new mutations.

The default root remains `access-dir/chat`, preserving history and Docker mounts.
`--chat-dir` selects the shared JetStream root; custom roots must enter backups
and must not be shared by running processes. Chat retention does not affect KV.
File/index overhead is additional to logical limits.

Cobra v1.10.2 (upstream commit `88b30ab`) supplies `serve`, `identity init`,
`owner init`, `owner reset-claim` and `version`; existing launch flags remain valid.
No Viper or implicit configuration search is introduced. Every Go internal package
has a dedicated doc.go describing its purpose/ownership. Desktop diagnostics expose
configuration/export without a Core probe or duplicate component version; real
build metadata retains development tags.

## Integration Evidence

Pinned nats-server v2.15.1 and nats.go v1.53.1 are recorded in go.sum. The actual
jetstream/kv.go Create/Update/Watch/Get implementation was inspected: Watch includes
initial values, and Get maps tombstones to not-found, so only new buckets import.
Tests use real embedded JetStream for restart, competing CAS claims, corrupt/deleted
state, legacy repair/import, interest projection, cancellation and nonblocking reads.
Native tests cover permission/CRUD/restart and chat/voice isolation. These are
correctness checks, not evidence of lower gaming CPU or latency.

References: [NATS KV](https://docs.nats.io/nats-concepts/jetstream/key-value-store),
[Cobra v1.10.2](https://github.com/spf13/cobra/releases/tag/v1.10.2), ADR-0026,
ADR-0031 and ADR-0032.
