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

Resona uses this interface for passive login channel snapshots and ordered
membership updates. It does not claim full voice or long-running compatibility.
See docs/adr/0005-read-only-ts3-session.md in the Resona root.

Run tests here separately: `go test -race ./...`. The parent module's tests do
not traverse this nested upstream module. Preserve this patch/provenance when
upgrading; remove the patch if upstream provides an equivalent interface.
