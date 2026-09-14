# ADR-0026: Scoped Resources and Foreground Synchronization

Date: 2026-09-14

## Decision

Treat resource observation and voice relay as distinct responsibilities. A resource
is not necessarily cosmetic: current-channel member instances/epochs, channel
bitrate, local session state and permissions also protect audio and commands.
These remain live while the GUI is inactive. Voice packets keep their existing
ring queues, format, encryption and forwarding path.

Native bootstrap includes only the current channel and its members. A
`WatchResources` command selects channel and member collections; current-channel
data, server identity and the caller's permissions are always included. All-member
interest requires all-channel interest. The desktop requests the full collections
while its active-session channel sidebar is open, or details need an object outside
the current channel. Otherwise it requests the current-channel scope. This is the first concrete
interest model, not a generic Kubernetes API or arbitrary object selector engine.

The command atomically replaces the watch and queues a complete snapshot of that
scope before its reply on the same reliable control stream. Later changes emit
new scoped snapshots immediately; unchanged scoped state emits nothing. A strictly
increasing per-connection revision prevents rollback. Snapshots replace the prior
scope, so omitted objects are not interpreted as deleted from the entire server.
The existing command bucket bounds explicit list requests. Delta object events,
per-resource versions and independent resource indexes remain future work.

The core keeps bounded chat history and current authoritative state while inactive.
It does not forward ordinary resource updates or meter/speaking animations to GPUI,
and does not repeatedly clone resource/history arrays for background meter events.
Lifecycle, permissions, command replies and voice controls remain urgent. Notification
sounds use a separate lightweight path in the background. No queue of every
intermediate resource event is retained. Chat is cumulative and must not be
coalesced as if it were a replaceable resource.

Local playback preferences for temporarily unobserved members are bounded to 128
entries and keyed by session, member ID and instance. A full list confirming a
departure prunes the entry; scope changes must not reset user volume preferences.

On activation or interest expansion, one serialized worker lists and watches the
latest scope. GPUI temporarily blocks resource actions until the matching interest
generation completes; the voice toolbar stays available. Foreground synchronization
also runs every five minutes through the same worker. The timer stops while inactive.
Failed synchronization is reported and retries on the long interval or the next
interest change, without an immediate retry loop. Session changes reapply interest;
stale completions are discarded. A watch reply timeout does not close a healthy
voice connection; cancellation of a blocked control write still closes the transport.

Inactive currently means GPUI reports the window is not active, including an
unfocused but still visible window. This deliberately optimizes the gaming case;
it is not OS occlusion detection. Pending old notification sounds are not replayed
when activation restores state. Automatic reconnection is outside this change.

## Protocol and Delivery

The user authorized breaking changes while matching client/server versions are
distributed together. Noise uses `resona-noise-exp-5` / `RN05`.
Deploy both endpoints together; v0.0.3 servers are not compatible with RN05
clients. The authorized development deployment was upgraded on 2026-09-15 and
verified with a real handshake, identity login and resource subscription.

Go operational logs use `log/slog` on stderr. CLI result output (`--version`, public
key initialization and explicitly requested claim codes) stays on stdout. IPC stdout
contains protocol JSON only; do not log credentials, private keys or chat contents.
