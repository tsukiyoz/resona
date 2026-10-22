# SpeexDSP Source Subset

Source: https://downloads.xiph.org/releases/speex/speexdsp-1.2.1.tar.gz

Release: 1.2.1. Downloaded 2026-09-08. Archive SHA-256:
`8c777343e4a6399569c72abc38a95b24db56882c83dbdb6c6424a5f4aeb54d3d`.

The C sources and upstream headers are copied unchanged. Only the acoustic echo
canceller (`mdf.c`), speech preprocessor, filterbank, and small FFT dependencies
are included. `COPYING` contains the upstream BSD license and source files retain
their original copyright notices. No codec, jitter buffer, or resampler is built.

Resona supplies `processor.go` and a portable `speexdsp_config_types.h` in place of
the configure-generated header. cgo compiles floating-point code with SMALLFT;
there is no runtime shared-library dependency or optional download on startup.

Integration uses 48 kHz mono, 960-sample frames, and a 200 ms AEC tail. The actual
stereo output callback is downmixed to provide the reference, after output volume
and local receive ducking. Noise attenuation settings are -10/-20/-30 dB.
Residual echo suppression settings are -40 dB, or -15 dB during near-end speech.
The reference excludes other applications and Resona's separate notification
player. Independent device clocks and hardware output delay still require device
listening tests; synthetic attenuation tests do not certify room performance.

Official API references:

- https://www.speex.org/docs/manual/speex-manual/node7.html
- https://www.speex.org/docs/api/speex-api-reference/group__SpeexPreprocessState.html

This denoiser is statistical speech processing. It is not a keyboard-event
classifier and cannot guarantee elimination of transient typing sounds.
