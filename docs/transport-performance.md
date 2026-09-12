# QUIC / Noise UDP local comparison

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
reordering experiment is claimed. Noise's simpler transport still needs automatic
rekey and congestion adaptation; see ADR-0019.
