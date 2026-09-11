# ADR-0017: Local per-member playback control

Status: Accepted (2026-09-11)

## Context

Users need to mute or change one participant's listening volume without affecting other listeners, capture devices, or server permissions. TS3 numeric client IDs can be reused after departure.

## Decision

Keep 0–200% volume and an independent mute flag in the connection's user snapshot. The local IPC command requires session ID, user ID, and member instance. The TS3 reducer allocates an instance on entry; rename and channel movement preserve it. Reentry and a new connection reset preferences. No preferences are persisted.

The service publishes an immutable playback table to the Go audio engine through an atomic pointer. The mixer validates packet/speaker instances and applies peer gain before summation, retaining global gain and limiting. Muted speakers still consume buffered frames. This changes no wire protocol or audio device lifecycle. Already mixed output has normal playback-buffer latency.

GPUI exposes confirmed values in member details with ten-percent steppers, mute, and reset. One control request may be in flight. Replies update only matching member playback fields, never roll back a newer workspace snapshot. Server detail permission failures are independent of these local controls.

## Consequences

Tests cover mixed PCM isolation, atomic publication, reused IDs, service validation, and the shared IPC shape. A fresh member-entry event intentionally resets the instance even if its numeric ID and nickname match. Windows multi-user listening validation remains necessary.
