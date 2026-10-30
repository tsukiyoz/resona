# Resona GPUI patch

This is the crates.io `gpui` 0.2.2 source package, checksum
`979b45cfa6ec723b6f42330915a1b3769b930d02b2d505f9697f8ca602bee707`.
Its published VCS metadata records Zed commit
`69e2130295c2649963eb639fc70b4f2ee8ea1624` with `dirty: true`;
the package checksum, not an assumed clean Git checkout, identifies the source.
The Apache 2.0 license is retained. Cargo's extraction marker is omitted.

The macOS change is in `src/platform/mac/window.rs`:
release `window_state` before invoking `resignKeyWindow`. AppKit can synchronously
send `windowDidResignKey`, whose delegate callback takes the same mutex.
Calling AppKit while holding it deadlocked the macOS main thread during an actual
offline Resona window activation on 2026-10-21. Audio/core workers were parked,
and no connection or device operation was involved.

In `src/platform/windows/events.rs`, apply the system title-bar appearance before
notifying appearance observers. Resona restores its explicit light/dark preference
in that observer; applying the system default afterward would overwrite it.
The reported window appearance remains the system appearance so follow-system
mode continues to work.

In `src/taffy.rs`, explicitly type the grid's zero length and fraction as `f32`.
Rust 1.99 warns about the previous float fallback and will eventually reject it;
the resulting layout values are unchanged.

`desktop/Cargo.toml` patches this exact crate for the entire desktop dependency
graph. Do not edit Cargo registry caches or apply patches during a build. Remove
this directory and the Cargo patch after an evaluated upstream release includes
the fixes and passes equivalent desktop tests, macOS activation/quit checks and
Windows light/dark/system title-bar checks. No dependency version is changed.
