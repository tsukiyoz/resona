# Resona Upstream Snapshot

- Source: https://github.com/HoneyBBQ/teamspeak-go
- Commit: a334def898f4d9c518a1434a9b9e9f889dcae954
- Imported: 2026-09-07, tracked files only, without Git metadata.
- License: MIT; original LICENSE and copyright notices retained.

Local changes: IncomingCommand / CommandObserver in types.go, constructor-only
WithCommandObserver in client.go, synchronous dispatch with one cloned Params
map per observer in commands.go, and observer_test.go. Existing routing remains
unchanged. A second guard in notifications.go prevents nickname-based guesses
from replacing an already-known authenticated client ID; the regression is
covered in observer_test.go. Observers receive already-unescaped parameters and must not
block waiting for network operations.

commands.go also adds ExecCommandContext for cancellable throttling and response
waiting, and CommandError with a numeric ID and the existing error text/sentinel.
The duration-based command methods share the same implementation and preserve
the command-timeout sentinel; their deadline now includes throttling. Canceling
does not retract an already-sent command. Tracker cleanup removes collected rows,
and resolving a command retires its pending entry so late/duplicate replies cannot
block the packet reader. These cases are covered in command_test.go.

helpers.go preserves shared movement context when splitting batched
notifycliententerview, notifyclientleftview, and notifyclientmoved commands.
Only cfid, ctid, reasonid, reasonmsg, invokerid, invokername, invokeruid, and
bantime inherit from the first row; explicit row values take precedence.
Member identities and attributes remain local to each row. The existing command
parser and escaping API preserve escaped values. Initial subscription snapshots,
batch movement, overrides, and unrelated commands are covered in
member_rows_test.go. An upstream TS3AudioBot connection trace demonstrates the
shared first-row context: https://github.com/Splamy/TS3AudioBot/issues/283.

Resona uses this interface for passive login channel snapshots and ordered
membership updates. It does not claim full voice or long-running compatibility.
See docs/adr/0005-read-only-ts3-session.md in the Resona root.

Run tests here separately: `go test -race ./...`. The parent module's tests do
not traverse this nested upstream module. Preserve this patch/provenance when
upgrading; remove the patch if upstream provides an equivalent interface.
