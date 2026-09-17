# Optional WebRTC audio backend

`libresona_webrtc_apm` supplies AEC3 and WebRTC NS on microphone input, plus a
separate AGC2 state for each decoded voice member. The Core loads the library
on demand through a versioned C ABI. The desktop never receives PCM through
IPC. All three backends remain disabled by default.

The integration uses `webrtc-audio-processing =2.1.0` with its bundled C++
WebRTC APM source. `test/audio3a` validated that dependency before production
integration (benchmark source commit
`c14d7af1760baff83e8210fee336a0cae0faaa7d`). Its API requires 10 ms
48 kHz mono frames, so this adapter divides each 20 ms voice frame into two
sequential subframes. Input and output buffers are owned by Go; the Rust
library copies a frame and does not retain Go memory across the C ABI call.

`make build-core` builds a release library with bundled static Abseil and
places it next to `build/bin/resona-core`. The macOS package puts the library
in `Resona.app/Contents/Frameworks` and signs it before the app. Windows uses
MINGW64 for this DLL, UCRT64 for Go CGO, and MSVC for GPUI; the C ABI owns
every allocation on the same side that frees it. See `docs/windows-build.md`.

On an Apple M2, `go test ./internal/audio -run '^$' -bench
'Benchmark(WebRTCInputChain|SpeechProcessing)$' -benchtime=300ms -count=3`
with `RESONA_WEBRTC_PLUGIN_DIR=build/bin` measured approximately 314-320 us
per 20 ms frame for AEC3+NS versus 150-157 us for Speex AEC+ANS (2026-09-17).
The Go allocation counter was zero in both; it does not count allocations
inside the Rust/C++ library. These local measurements are not a Windows game
workload or a quality comparison. Disabling all modules avoids loading the
native library and adds no processing loop.

Builds include the dependency notices collected by `licenses.py`, including
the bundled WebRTC, Abseil and third-party notices. The core still works
without the optional library; it hides the WebRTC choices and refuses to
apply previously saved choices until a matching library is available.
