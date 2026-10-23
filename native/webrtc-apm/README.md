# Native WebRTC and RNNoise audio backend

`libresona_webrtc_apm` supplies AEC3 and WebRTC NS on microphone input, plus a
separate AGC2 state for each decoded voice member. The Core loads the library
on demand through a versioned C ABI. The desktop never receives PCM through
IPC. The same library includes the optional RNNoise standard-model ANS (ABI 2,
kind 4); source/model preparation and provenance are in `../rnnoise/README.md`.
New preferences use WebRTC AGC2; existing preferences remain unchanged.

The integration uses `webrtc-audio-processing =2.1.0` with its bundled C++
WebRTC APM source. `test/audio3a` validated that dependency before production
integration (benchmark source commit
`c14d7af1760baff83e8210fee336a0cae0faaa7d`). Its API requires 10 ms
48 kHz mono frames, so this adapter divides each 20 ms voice frame into two
sequential subframes. Input and output buffers are owned by the Rust client;
the backend copies a frame and retains no caller memory across the C ABI call.

`make build-apm` builds a release library with bundled static Abseil and
places it in `build/bin/` for development. The macOS package puts the library
in `Resona.app/Contents/Frameworks` and signs it before the app. Windows uses
MINGW64 for this DLL and MSVC for GPUI; the C ABI owns
every allocation on the same side that frees it. See `docs/windows-build.md`.

The current comparative DSP benchmark is `test/audio3a` (see its README for
build requirements and fixtures). Product integration is exercised by
`RESONA_TEST_NATIVE_AUDIO=1 RESONA_WEBRTC_PLUGIN_DIR="$PWD/build/bin" make test-client`.
Disabling all modules avoids loading the native library and adds no processing
loop. DSP timing alone is not a Windows game workload or a quality comparison.

Builds include the dependency notices collected by `licenses.py`, including
the bundled WebRTC, Abseil and third-party notices. The core still works
without the optional library; it hides the WebRTC choices and refuses to
apply previously saved choices until a matching library is available.
