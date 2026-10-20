# RNNoise microphone suppression

The ANS slot offers the upstream **regular** model as `rnnoise`. This is the
model preferred over little for background speech in the recorded listening
feedback. SpeexDSP remains the default; saved selections are not changed.
AEC and per-member receiving AGC are independent of this choice.

Source: https://github.com/xiph/rnnoise at
`70f1d256acd4b34a572f999a05c87bf00b67730d`.
Model archive SHA-256:
`0a8755f8e2d834eff6a54714ecc7d75f9932e845df35f8b59bc52a7cfe6e8b37`.
These are the exact source/model evaluated by `test/audio3a`, including the
ANS -> product gopus -> per-member AGC2 chain on macOS and Windows.
See `test/audio3a/VERIFICATION.md` for measurements and their limitations.

`make build-core` prepares and verifies the source/model into the ignored
`build/native/rnnoise` cache, then compiles RNNoise into the existing native
audio library. Windows builds run the same preparation script. No model is
downloaded at runtime; packages include the RNNoise license and authors.
Set `RNNOISE_SOURCE` to reuse an existing verified checkout. The initial
build requires Git, curl, tar and a roughly 56 MiB model archive download.

The library retains its filename `resona_webrtc_apm` and extends its C ABI to
version 2: processor kind 4 is RNNoise. Older ABI 1 supports WebRTC only and
does not advertise RNNoise. State is created on the control path, transferred
to the existing encoder worker and destroyed by that worker. Each 48 kHz
mono 20 ms packet is split into two sequential 10 ms calls with PCM16-scale
floats. State persists between packets. There is no PCM IPC, additional worker,
per-frame allocation, GPU requirement or download thread. RNNoise's internal
algorithmic delay still applies; do not equate frame processing time with delay.

The selected ANS has no strength parameter. Backend changes clear incompatible
parameters; unsupported slot/parameter/library combinations fail explicitly.
The same on-demand native library is shared with WebRTC. Capability discovery
briefly loads/unloads it to check ABI 2; Speex-only processing creates no
RNNoise state and keeps no library handle. This first integration does not introduce little, external
weights, model training, or a change to the default processing chain.
