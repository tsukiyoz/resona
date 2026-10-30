# Official Opus build

`make build-opus` fetches and verifies the pinned Opus 1.6.1 commit in `build.sh`,
then installs a release static library under `build/native/opus-install`.
The desktop and Rust client use its pkg-config metadata with `OPUS_STATIC=1`.

`apple-neon.cmake` runs immediately after Opus's CMake `project()` declaration.
For a macOS ARM64 target, it selects always-available NEON rather than runtime
CPU detection, which Opus 1.6.1 does not implement on Darwin ARM. It also supplies
the NEON availability definitions required by the headers when runtime detection
is off. This keeps the optimized sources enabled; it does not disable SIMD,
change the codec version, or suppress compiler diagnostics.

Explicit Intel cross-builds and non-Darwin targets retain upstream defaults.
The existing native build script does not produce a universal macOS library.
`make test-build` checks target selection, required definitions and the explicit
intrinsics-disabled case without compiling Opus. Real codec regression tests
are in `client/src/audio/codec.rs`.
