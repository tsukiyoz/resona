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

Playback refills follow the output ring's remaining sample count, not relative
20ms timer deadlines. Each worker pass writes at most two frames, refilling below
40ms of queued PCM (less than 60ms immediately after a refill). Device callbacks
wake the worker; the 20ms timed wait is only a fallback for stopped callbacks.
No high-resolution global timer, busy wait or new audio thread is required.

Route changes publish a discard boundary for playback/monitor samples. Only the
callback advances read cursors, dropping queued old-route PCM while preserving
new-route writes. Capture epochs remain separate, so releasing PTT never flushes
remote playback. Device handles and outgoing DSP survive in-session route changes.

`device_test.c` checks callback/ring behavior without opening hardware, including
partial output periods and ring wrap. Rust engine tests additionally simulate
one/two remote speakers with 7/16/31ms worker wakes and continuous device consumption.

Native device stop/uninit remains an OS-driver call. In-process ownership must
be tested for cancellation and slow driver operations before product cutover;
the former helper-process kill fallback does not transfer to this adapter.
