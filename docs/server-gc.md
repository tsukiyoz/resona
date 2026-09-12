# Native server GC investigation

Date: 2026-09-12. Apple M2, 16 GiB RAM, Go 1.26.4 darwin/arm64.

## Measurement boundary

`internal/server/gcprobe_darwin_test.go` launches an isolated server subprocess
and uses the normal native client adapter from the parent load generator. The
server is real Noise UDP on a fresh loopback port, with a temporary in-memory
identity. It does not touch the Docker demonstration or user bookmarks.

The server CPU, heap, allocation and GC measurements exclude the load generator.
Both processes still compete for the same host; these are local measurements,
not a remote deployment capacity test. The test binary links client dependencies
but the server child does not instantiate clients, devices or codecs.

A test-only connection wrapper stamps a reserved field in each 80-byte synthetic
voice payload. Forwarding latency starts when the server application obtains a
decrypted datagram, and ends after downstream encryption and socket submission.
It includes the shared server lock and outbound queue, but excludes time waiting
in the kernel or transport receive queue and decrypting the inbound packet.
Client end-to-end latency additionally captures those earlier stages and uses
the shared host wall clock; server forwarding uses a monotonic clock.

Latency histograms have 10 us buckets through 50 ms and an overflow counter;
reported percentiles are bucket upper bounds. Overflow percentiles are null,
not 50 ms. Exact maxima are tracked separately. Receive counts exclude silence
end markers. Voice surrogates are not encoded/decoded Opus or acoustic tests.

Runtime metrics and forwarding-window maxima are sampled every 100 ms to a
buffered file; records are not accumulated in server memory. The instrumentation
adds counters, timestamping, sampling and small allocations. GC pause histograms
cover the full interval; exact per-cycle pause values retain only the last 256
cycles, as limited by runtime.MemStats. A cycle can contain multiple pauses.
GC completion in the same 100 ms window as a spike is correlation, not causation;
GC work can also span window boundaries. GC CPU includes background/idle workers.

Every mode starts from a forced collection after login bootstrap and before the
measured interval. No forced collections occur inside the interval. The child
has `GOMEMLIMIT=256MiB`, a soft Go-runtime memory budget, not an OS RSS cap.
`GOGC=off` disables the heap-growth trigger but does not disable this memory limit.
Zero GC must therefore be confirmed from measured counters. It is limited to
45-second diagnostic runs and is not proposed as a production setting.

All scoring runs use normal optimizations without race/profiling. Separate short
race checks validate the harness and reconnect cleanup; their latency results
are excluded. Trace/CPU/allocation profiles are separate diagnostic runs.

## Reproduce

From the repository root on macOS:

```sh
sh tools/run-server-gc-probe.sh build/bin/gc-probe
node tools/summarize-server-gc.mjs build/bin/gc-probe
```

The script builds a reusable test executable and alternates three 10-second
default/off pairs. Each pair uses 64 members, four channels of 16, four senders
per channel, 50 packets/second/sender, 80-byte payloads. Each run should receive
120,000 forwarded packets. Setup and the final 200 ms drain are recorded separately
from the nominal 10-second sending duration (CPU denominator includes the drain).

For a continuous run with one listening member reconnecting every two seconds:

```sh
RESONA_SERVER_GC_PROBE=1 RESONA_GC_SECONDS=1800 \
RESONA_GC_MEMBERS=64 RESONA_GC_ROOMS=4 RESONA_GC_CHURN=1 \
RESONA_GC_OUTPUT="$PWD/build/bin/gc-probe/long" \
build/bin/gc-probe/server-probe.test -test.run='^TestServerGCProbe$' \
  -test.v -test.timeout=35m
```

The stable-member delivery counter excludes the reconnecting listener, whose
absence makes its expected delivery count inherently variable. Diagnostic profiles
use `RESONA_GC_DIAGNOSTIC=1` with a short duration and a separate output directory.
No production server flags, protocol fields or buffer ownership are changed.

Raw records and profiles live under ignored `build/bin/gc-probe-20260912/`.
Initial `default-1` and `off-1` samples predated the bootstrap cleanup correction;
`off-1` hit the memory limit and collected once. Use `ab-*` for the paired results.

## Paired results

Median of three equal 10-second runs per mode, fresh server processes, 64 members:

| Metric | Default GOGC=100 | GOGC=off diagnostic |
| --- | ---: | ---: |
| Server CPU, one-core % | 26.80 | 25.46 |
| Allocation MiB/s | 14.55 | 14.52 |
| End-of-window Go heap MiB | 1.53 | 149.26 |
| GC cycles | 68 | 0 |
| Cumulative GC pause ms | 7.63 | 0 |
| GC assist CPU ms | 30.68 | 0 |
| Forwarding p99 upper bound ms | 5.43 | 5.26 |
| Forwarding p99.9 upper bound ms | 7.97 | 11.25 |
| Client end-to-end p99 upper bound ms | 6.41 | 6.33 |

All six runs delivered 120,000/120,000 expected packets each. The largest single
GC pause histogram upper bound across the default runs was 0.393216 ms. Pauses
totaled approximately 0.075% of elapsed time, and assist used approximately 0.30%
of one CPU core. These are different metrics, not quantities to sum as latency.

Turning GC off did not consistently improve tail latency: per-run forwarding
p99.9 ranges overlap (default 6.42-13.79 ms, off 8.35-12.86 ms). The median being
higher with GC off does not prove GC improves latency. Three short trials are
insufficient to resolve small scheduler-dependent differences. They do show that
GC is not necessary for the observed multi-millisecond tails.

## Allocation profile

A separate 20-second trace/CPU/allocation run included nine listener reconnects.
Its performance values are not pooled with unprofiled measurements. The sampled
allocation profile includes login bootstrap and recorded 405.19 MiB cumulatively:

- `nativewire.SendVoiceQueue` accounted for 66.52% cumulatively, including its
  downstream calls. This is not a flat allocation percentage.
- The flat allocations attributed to context propagation/Done/deadline/AfterFunc
  and timer creation together accounted for about 60%. These do not overlap when
  summed as flat entries.
- `nativewire.EncodeVoice` directly allocated 4.44%; `noiseudp.Conn.send` directly
  allocated 10.37%. Pooling only these byte buffers would miss much of the churn.
- CBOR encoding and membership snapshots also appeared because bootstrap and
  reconnect state broadcasts are part of this diagnostic profile.

The current shared send queue creates a deadline context and cancellation callback
for every packet; Noise also has transport write-deadline/gate logic. This is a
concrete optimization candidate, but removing timeout objects must preserve
bounded writes, cancellation, nonce ownership and shutdown behavior. No such
production changes or pooling were made during this investigation.

The first full-capacity reconnect diagnostic attempted an immediate reconnect
before the server released the old admission slot and was rejected. The revised
workload waits 150 ms after local close. This is a workload boundary observation,
not attributed to GC. The failed profile directory is excluded from results.

## Thirty-minute run

The unprofiled run completed in 1,800.76 measured seconds with 64 members,
four rooms, sixteen active senders and 900 listener reconnects. No connection
workflow failed. It sent 1,440,000 source voice packets.

| Server measurement | Result |
| --- | ---: |
| CPU, one-core % | 29.10 |
| Allocation MiB/s | 16.83 |
| End-of-window Go heap MiB | 2.55 |
| Largest 100 ms sampled Go heap MiB | 3.27 |
| GC cycles | 15,336 |
| Cumulative GC pauses | 1,876.13 ms (0.104% of elapsed time) |
| Single GC pause p99 bucket upper bound | 0.32768 ms |
| Worst single GC pause bucket upper bound | 14.680064 ms |
| GC assist CPU | 8,265.49 ms (0.459% of one core on average) |
| Runtime-reported GC CPU, including idle work | 45,955.01 ms |
| Forwarding p99 / p99.9 bucket upper bounds | 5.54 / 12.51 ms |
| Forwarding exact maximum | 218.955 ms |
| Client end-to-end p99 / p99.9 upper bounds | 6.46 / 15.44 ms |
| Client end-to-end exact maximum | 222.197 ms |

RSS snapshots at approximately 9.5 and 20.9 minutes were 20,896 and 21,824 KiB,
respectively, for the isolated server child. These are two observations, not a
continuous RSS peak measurement. Other host applications were not forcibly
closed, so maxima include the behavior of a shared macOS host.

Stable members received 21,231,127 of 21,240,000 expected deliveries: 8,873 missing
(approximately 0.0418%). The reconnecting listener is excluded from both counts.
The server recorded 637 forwarded packets taking at least 50 ms; client callbacks
recorded 1,513 such deliveries. This run completed, but it did not establish
zero loss or consistently low maximum latency. Drop locations are not instrumented
and cannot yet be attributed to GC, outbound queue drops, stale-packet expiry,
receive queues, sockets or scheduling.

There were 52 sampled windows with a forwarding maximum over 20 ms: 37 had a GC
completion and 15 did not. Most windows overall had a GC completion (13,773 of
18,006), so coincidence by itself is weak evidence. The largest forwarding spike
was in a window near 387.7 seconds with one GC completion and about 1.09 ms of
aggregate GC assist. The long run did not record a full execution trace; the
short diagnostic trace cannot identify the cause of that specific event.

The short diagnostic scheduling profile attributes most runnable delay to channel
wakeup sites (`runtime.selectnbsend`, `runtime.chanrecv1`), including the voice
queues and shared Noise write gate. This identifies paths worth investigating,
not a proof of a channel defect or a measurement of lock-hold time.

GC average pause cost is small and memory stayed bounded in this workload, but
the worst pause histogram invalidates a claim that pauses are always sub-ms.
Short zero-GC trials also retained multi-ms forwarding tails. Neither result
establishes a hard latency guarantee or a reason to rewrite the server today.

## Interpretation and next optimization

Reducing short-lived allocations normally reduces GC work, but pool usage alone
does not establish latency bounds. `sync.Pool` entries may disappear; it is a
reuse cache, not guaranteed reserved storage. Keep pool sizes controlled and
return buffers only after every asynchronous consumer has finished. Reusing an
in-flight packet buffer would trade allocation costs for data corruption/races.

Priority for a separate implementation change:

1. Reduce per-packet context/timer/callback creation while retaining the same
   stalled-write and shutdown guarantees. Measure before/after with this harness.
2. Evaluate bounded per-worker/per-connection buffers for encryption and packet
   encoding, with explicit ownership; use a shared pool only where it helps.
3. Profile lock and queue delays separately. Buffer reuse will not remove the
   global membership lock, fan-out work or OS scheduling delays. Add distinct
   counters for outbound queue drops, expiry and receive drops before attributing
   the long-run missing deliveries to any one source.

Do not disable GC, add an arbitrary low memory limit, or rewrite the server in
Rust based solely on the presence of GC. The current measurements justify reducing
allocation churn, but have not established GC as the dominant source of tail
latency. Long-run results and remaining limits should guide the next decision.

References: [Go GC latency guide](https://go.dev/doc/gc-guide#Latency),
[sync.Pool contract](https://pkg.go.dev/sync#Pool).
