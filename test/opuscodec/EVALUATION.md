# Initial local evaluation: 2026-09-16

This experiment does not justify rewriting the core in Rust. Official C libopus
is the most promising next codec candidate on this machine; production gopus
remains unchanged. The two pure Rust implementations do not provide an overall
encode/decode advantage in this workload.

## Reproduction and scope

- Apple M2, macOS arm64, Go 1.27.1, Rust 1.96.0, C libopus 1.6.1.
- gopus 0.1.1, opus-rs 0.1.33, rusty-opus 0.9.1; source pins in README/lockfiles.
- Release builds, default Rust CPU target; Homebrew C library. This compares the
  available implementations/builds, not programming languages under identical
  compiler optimization. C and Rust calls include cgo overhead.
- 48 kHz mono, 20 ms, VoIP, complexity 5, FEC/DTX off. Five repeats, each with
  1 s warmup and 15 s measured input (750 ticks). Execution order rotates.
- Public `answer_16k.wav` from the pinned opus-rs source, converted once to 48 kHz
  f32le. Upsampling does not turn the 16 kHz recording into full-band speech.
- Exact measured-input prefix SHA-256:
  `e257ea26b9a7dc8244f79bfc1e987f0d120b6de9a36cce3c98365ae9af79b571`.
- Local raw reports: `build/opuscodec/20260916T085808Z/` (CBR) and
  `build/opuscodec/20260916T090016Z/` (VBR). These generated files are not tracked.

Commands and fixture provenance are documented in README.md. There is one speech
recording, no listening panel, packet loss, device processing, mixing, network,
mobile or Windows measurement. Results are preliminary and platform-specific.

## CBR at 32 kbps

Each packet was verified as 80 bytes. Times are the median of five per-repeat
means, in microseconds per 20 ms tick. Decode 8 includes eight independent
decoders, all receiving the same official-libopus packet sequence.

| Implementation | Encode 1 | Decode 1 | Decode 8 | Encode CPU % | Decode 8 CPU % |
| --- | ---: | ---: | ---: | ---: | ---: |
| gopus | 161.29 | 22.47 | 163.65 | 0.802 | 0.814 |
| C libopus | 127.99 | 20.80 | 152.38 | 0.659 | 0.778 |
| opus-rs | 137.40 | 28.47 | 215.30 | 0.727 | 1.100 |
| rusty-opus | 126.73 | 27.05 | 203.69 | 0.665 | 1.048 |

CPU percentages normalize measured process CPU time to real-time 20 ms ticks;
one core is 100%. They are not live desktop CPU measurements. Separate encode
and decode rows cannot establish a live mixed-call tail latency.

C libopus encoding is about 21% faster than gopus by mean wall time; single-stream
decoding is about 7% faster. Rusty-opus encoding is about 21% faster, but its
eight-stream decoding is about 24% slower. At one encode plus eight decodes,
the sum of measured CPU budgets is approximately 1.62% of one core for gopus,
1.44% for C libopus and 1.71% for rusty-opus. This small absolute difference is
not evidence of a material gaming frame-time improvement.

The empty cgo bridge averaged about 0.08 us in this run, versus 20-160 us for a
codec call. This excludes IPC and says nothing about the cost of the existing
desktop/core process boundary. Replacing a codec does not require replacing Go.

## Correctness and bitrate qualifications

All four implementations produced packets and decoded complete frames. However,
opus-rs decoder output had NRMSE around 0.22 against C libopus on the same packet
streams. gopus and rusty-opus were below six-decimal display precision in this
corpus. The opus-rs discrepancy also appeared in the synthetic smoke test.
Its root cause is not established: this is a failed investigation gate, not a
claim of RFC nonconformance or an upstream defect. It is not eligible for a
transparent replacement until investigated. Both formal runs intentionally
return nonzero while preserving their reports because of this gate.

All CBR speech packets selected Hybrid mode. Encoder waveform SNR at 32 kbps
ranged from 5.70 to 6.00 dB after delay alignment. This diagnostic does not prove
equal perceptual quality and must not be used to rank codecs.

The exploratory VBR run illustrates why matching the bitrate control alone is
insufficient. C/Go used constrained VBR; Rust implementations used their internal
mode-dependent rate control:

| Implementation | Target kbps | Actual kbps | Encode mean us | Decode 8 mean us |
| --- | ---: | ---: | ---: | ---: |
| gopus | 32 | 32.60 | 167.45 | 168.39 |
| C libopus | 32 | 32.65 | 123.77 | 165.56 |
| opus-rs | 32 | 50.83 | 138.48 | 220.63 |
| rusty-opus | 32 | 35.11 | 119.51 | 211.60 |

## Decision

Keep the current product dependency for now. If further codec optimization is
prioritized, evaluate C libopus first in a separate branch: Windows/macOS release
builds, more speakers and full-band material, silence/music, FEC/PLC/DTX, listening
checks and a complete device-to-network audio profile. Neither this microbenchmark
nor implementation language establishes that Opus is the largest whole-client CPU
consumer. A Rust core should be evaluated separately for ownership, platform reuse
and lifecycle benefits, with migration cost included.
