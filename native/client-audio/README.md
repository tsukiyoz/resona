# Rust client device adapter

`miniaudio.h` is the unmodified miniaudio 0.11.25 header already used by
`github.com/gen2brain/malgo v0.11.26`. SHA-256:
`ac7af4de748b7e26b777f37e01cee313a308a7296a3eb080e2906b320cc55c89`.
Its public-domain/MIT-0 license is included at the end of the header.

`device.c` retains the same 48 kHz mono input / stereo output device format,
period and device-ID encoding. All rings have eight frames. Callbacks copy
samples through miniaudio's SPSC ring and wake the owning Rust worker; they
do not allocate, call codecs/DSP, wait for a mutex, or access the network.
Capture frames carry gate epochs and age, so PTT releases and stalled workers
cannot replay old microphone input. Local monitor audio is mixed after the
AEC reference is recorded, as in the existing Go engine.

Native device stop/uninit remains an OS-driver call. In-process ownership must
be tested for cancellation and slow driver operations before product cutover;
the former helper-process kill fallback does not transfer to this adapter.
