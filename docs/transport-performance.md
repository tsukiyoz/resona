# QUIC / Noise UDP local comparison

## Latest rerun: 2026-09-14

Latest uncommitted source on `feature/tsukiyo/server-20260912_voice-watchdog`,
including Protobuf control (ADR-0021), per-peer mutex MPSC rings and background
Noise rekey (ADR-0020). Apple M2, macOS 26.5.1 (25F80), Go 1.26.4 darwin/arm64.
The short sample windows do not exercise scheduled key rotation.

The workload and measurement definitions below still apply. This rerun compiled
one optimized test executable without race instrumentation, then ran each leaf
subtest in a fresh process. Three sequential rounds used QUIC/Noise, Noise/QUIC,
QUIC/Noise order. All 18 cases completed successfully. Values are medians across
three runs, including medians of per-run latency percentiles, not pooled samples.

| Scenario | Transport | CPU % | IPv4 KiB/s | p50 ms | p95 ms | p99 ms | Alloc MiB/s |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 4 members, 1 sender | QUIC | 6.59 | 33.33 | 0.544 | 1.467 | 3.185 | 0.201 |
| 4 members, 1 sender | Noise | 3.73 | 27.16 | 0.389 | 0.669 | 1.015 | 0.094 |
| 16 members, 4 senders | QUIC | 27.14 | 498.52 | 1.563 | 3.763 | 9.286 | 2.923 |
| 16 members, 4 senders | Noise | 20.74 | 438.15 | 1.134 | 3.236 | 6.459 | 1.755 |

- Noise reduced aggregate CPU by 43% / 24%, traffic by 18% / 12%, and allocation
  rate by 53% / 40% in the 4-member / 16-member scenarios.
- Each transport delivered 4,500/4,500 and 90,000/90,000 packets respectively
  across the three rounds; every individual run matched its expected count.
- The 16-member p99 ranges overlap: QUIC 6.875-9.472 ms, Noise 4.826-8.707 ms.
  This supports a lower observed median, not a universal tail-latency guarantee.
- Whole-process goroutines remain 68 vs 57 (4 members), 248 vs 213 (16 members).
- Median end-window Go heap was 1.29 vs 1.71 MiB (4-member voice) and 4.59 vs
  3.04 MiB (16-member voice), QUIC vs Noise. GC phase affects these snapshots;
  they cannot establish an RSS or retained-memory ranking.

| Four idle members | CPU % | IPv4 KiB/s | End-window Go heap MiB |
| --- | ---: | ---: | ---: |
| QUIC | 0.051 | 0.0619 | 1.15 |
| Noise | 0.036 | 0.0509 | 0.77 |

Idle CPU is tiny for both; the difference is only 0.015 percentage points of one
core. All CPU, heap and allocation numbers include server, simulated clients and
counting relays. These are not server-only results or desktop/audio performance.
Neither the network conditions nor the load establish a high-concurrency ceiling.
The current Noise implementation remains the lighter measured local voice path;
QUIC and Noise do not provide equivalent congestion-control functionality.

Allocation rates are lower than the September 12 baseline, but several changes
and process isolation differ between runs. This is not an isolated attribution
to Protobuf, MPSC, rekey, or timer changes.

Raw records: `build/bin/transport-comparison-20260914-BesrKk/*.log` (ignored).
Test executable SHA-256:
`e878d9a1a427bc910a523301ed5fe7d256e233045707d2952d50ab551b03001c`.

Build with `go test -c -o <output>/transport.test ./internal/protocol/native`.
Run each combination separately, substituting round 1-3, scenario
`idle4`/`voice4`/`voice16`, and mode `quic`/`noise`:

```sh
RESONA_TRANSPORT_PERF=1 <output>/transport.test \
  -test.run='^TestTransportPerformance$/^1$/^voice4$/^quic$' \
  -test.v -test.timeout=60s
```

## Historical baseline: 2026-09-12

Date: 2026-09-12. Experimental implementations on the working branch, not a
comparison of all possible QUIC and Noise implementations.

## Method

- Apple M2, 16 GiB RAM, macOS arm64, Go 1.26.4. Native Go test binary with
  normal compiler optimizations, no race detector or profiler during measurement.
- Both transports use the same server membership/relay logic and native client
  adapter. Each subtest starts a fresh loopback server and clients with temporary
  trust material. The separately running Docker demo is not the test target.
- Three rounds, alternating transport order. Four connected idle clients;
  four clients with one sender; sixteen clients with four senders. All members
  share one channel and receive other senders. Active senders emit 500 frames
  each at 20 ms intervals, with a final 100 ms delivery drain.
- Voice payloads are 80-byte opaque surrogates (32 kbit/s at 50 frames/s), with
  a monotonic timestamp embedded for measurement. There is no Opus encoding,
  decoding, microphone, jitter buffer, GUI or audio-device measurement.
- A UDP relay per client counts datagram payload bytes and packets in both
  directions. Each original datagram is counted once, despite the extra local
  forwarding hop. IPv4 traffic adds 28 bytes per datagram for IP/UDP; Ethernet,
  tunnels and Wi-Fi framing are excluded. QUIC ACK/control/probe traffic and
  Noise control/keepalive traffic are included when inside the sample window.
- CPU is user+system process time divided by elapsed wall time; 100% means one
  fully occupied CPU core. It includes server, all clients, counting relays and
  test bookkeeping. These numbers are **not server-only or desktop CPU**.
- Allocation rate is Go TotalAlloc growth per second, not retained memory.
  `heap_mib` is end-of-window Go HeapAlloc, sensitive to GC timing; it excludes
  stacks, native allocations and other RSS components. Do not rank resident
  memory use from that field. Goroutines likewise cover the whole test process.
- Delivery latency includes both local relay hops and Go scheduling. Percentiles
  cover received packets only; expected/received counts are reported separately.
  Setup bytes include sequential joins, state broadcasts and settling, not just
  cryptographic handshake bytes. Connect times include application bootstrap.

## Reproduce

macOS only (the CPU sampler uses Darwin getrusage):

```sh
RESONA_TRANSPORT_PERF=1 go test ./internal/protocol/native \
  -run '^TestTransportPerformance$' -count=1 -v -timeout 10m
```

Each `PERF` line contains a JSON record. Normal test runs skip this benchmark.
The durable harness uses 30-second idle windows to cover multiple maintenance
ticks. Initial 10-second idle samples were discarded for comparison because
Noise's 5-second maintenance scan can place its first keepalive after that window.

## Voice results

Values below are medians of three per-run measurements (latency columns are
medians of per-run percentiles, not pooled percentiles). CPU uses one-core units.
Traffic is total upload + all forwarded downloads, including estimated IPv4/UDP.

| Scenario | Transport | CPU % | KiB/s | p50 ms | p95 ms | p99 ms | Alloc MiB/s |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 4 members, 1 sender | QUIC | 5.99 | 33.24 | 0.521 | 1.419 | 2.666 | 0.348 |
| 4 members, 1 sender | Noise | 3.52 | 27.17 | 0.385 | 1.067 | 1.648 | 0.257 |
| 16 members, 4 senders | QUIC | 26.37 | 498.35 | 1.590 | 3.780 | 5.613 | 5.337 |
| 16 members, 4 senders | Noise | 19.86 | 438.10 | 1.261 | 3.743 | 5.753 | 4.185 |

- Noise used approximately 41% / 25% less aggregate CPU and 18% / 12% less
  traffic in the small / larger scenarios. Allocation rates were 26% / 22% lower.
  This supports continuing the Noise experiment for compact voice relay.
- Tail latency is not a universal Noise win: the 16-member p99 was slightly
  higher, with overlapping run-to-run ranges. Both are sub-10-ms local paths;
  this does not predict WAN quality or audible end-to-end latency.
- Four-member deliveries: 4,500/4,500 for each transport. Sixteen-member
  deliveries: QUIC 89,996/90,000, Noise 90,000/90,000. Four missing QUIC deliveries
  occurred in one run (0.0044% overall). The harness does not identify whether
  they were dropped by a local queue, socket or transport, so this is not a
  demonstrated inherent QUIC loss characteristic. A passing benchmark means it
  completed; it does not assert zero loss.
- Whole-process goroutines were 68 vs 57 (4 members) and 248 vs 213 (16 members).
  End-of-window heap values varied with collection timing; no resident-memory
  winner is claimed. Allocation rate provides a more repeatable comparison here.
- Median join/bootstrap latency was 2.75 vs 2.56 ms (4-member scenario), and
  3.62 vs 2.77 ms (16-member scenario), QUIC vs Noise. Accumulated setup traffic
  was 72,936 vs 3,858 UDP payload bytes and 355,644 vs 81,483 bytes respectively.
  Setup includes state broadcasts and QUIC probes/padding; these are not isolated
  cryptographic-handshake measurements.

Raw local records: `build/bin/transport-comparison-20260912/active.json` and
`idle.json` (ignored build artifacts). The first file also preserves the discarded
10-second idle samples for transparency.

## Idle results

Three 30-second samples, four connected clients, median per-run measurements:

| Transport | Aggregate CPU % | Estimated IPv4 KiB/s | End-window Go heap MiB |
| --- | ---: | ---: | ---: |
| QUIC | 0.046 | 0.0619 | 1.17 |
| Noise | 0.030 | 0.0477 | 0.78 |

Both have very low idle CPU and exchange keepalives. The absolute CPU difference
is about 0.016 percentage points of one core, too small to infer a meaningful
gaming benefit from this short local run. Heap values are included as Go-runtime
observations only, not total process footprint or server-only memory.

## Scope

This is a low-load loopback protocol baseline, not a capacity ceiling, independent
security review or WAN result. The counting relays add CPU, scheduling and socket
work, and can affect transport batching. Absolute numbers should not be used as
deployment sizing. No Windows/Linux device, GPU, congestion, loss, jitter or
reordering experiment is claimed. The September 12 baseline predates automatic
rekey; background rekey is now implemented (ADR-0020). Congestion adaptation
remains separate work; see ADR-0019.
