# Socket writer and batch experiment

This is a disposable experiment, not a product transport option. The production
Noise source and its normal build are unchanged. `prepare.go` uses Go's AST and
JSON APIs to create a build overlay under ignored `build/`; the overlay starts one
socket writer per listener and joins it on listener shutdown.

The comparison isolates socket ownership. Existing per-member voice queues and
send workers remain. Writers rendezvous with the socket owner through an
unbuffered channel, then wait for a synchronous completion reply. Requests and
admission timers are pooled. Admission and socket I/O each retain their existing
250 ms bound; shutdown closes the socket and cancels pending admissions. The
caller cannot reuse ciphertext until the owner has finished. No packet copy,
extra buffered packet queue, idle scan or protocol change is added. The `writer`
model sends one packet per submission; the `batch` model below adds real batch I/O.

There is no explicit priority or business-level round-robin guarantee. Connected
peers retain sendMu, so each has at most one in-flight socket submission, but
handshake traffic can also submit requests. This is **not** the full ready-list
design that removes each member's sending worker and pulls their voice queue.
A negative result here cannot disprove every such design; it can show whether
adding a socket-owner handoff alone helps this workload.

Run from the repository root on macOS or Linux:

```sh
go run test/server-perf/socketwriter/prepare.go -output build/server-writer/overlay
go test -overlay "$PWD/build/server-writer/overlay/overlay.json" -race ./internal/noiseudp ./internal/server
sh test/server-perf/socketwriter/run.sh build/server-writer/my-run
```

The runner compiles both binaries before measurement, runs 64-member baseline /
writer / writer / baseline (ABBA), then a 4-member baseline / writer pair.
Each run is a new loopback server, has 3 seconds of actual voice warm-up and
30 seconds of measurement. Override `RESONA_GC_SECONDS` for a smoke test.
Use a new output directory each time. Do not compile, profile or run other load
tests concurrently. The source fixtures end in `.go.txt` so normal Go builds
cannot accidentally include them.

For higher pressure and longer repeats:

```sh
RESONA_WRITER_SUITE=stress sh test/server-perf/socketwriter/run.sh build/server-writer/stress-run
```

This keeps the 64-member product limit but places everyone in one channel, with
8 then 16 simultaneous 50 Hz speakers. Target forwarding rates are 25,200 and
50,400 datagrams/s (2.1x and 4.2x the quick suite's 64-member load). Each level
runs both models twice for 60 seconds; orders are ABBA then BAAB. The existing
product queue capacity (now sixteen), expiry and timeouts are unchanged. Track missing
deliveries as well as latency: dropped old packets can make the remaining ones
look faster. Values above the histogram's 50 ms limit still have an exact max
and overflow count, but unresolved percentiles remain null.

Every run retains a separate console log. Failures remain in the record and the
runner continues to the other model without automatically rerunning a failed
case. It returns failure after the suite if any case failed. Check target versus
achieved packet rates and actual elapsed time; an overloaded generator may not
sustain the requested rate. These are short load experiments, not soak tests.

Compare queue capacity independently of the socket model:

```sh
sh test/server-perf/socketwriter/matrix.sh build/server-writer/queue-matrix-run
```

This compiles four binaries (baseline/writer x 4/16 slots), then measures each
combination twice at both stress levels for 60 seconds. Each eight-run block
reverses its first four configurations, and the second stress level starts in a
different order. All four binaries include identical test-only queue counters:
enqueue and overwrite counts under the existing queue mutex, plus an atomic
counter only when stale voice is discarded. Boundary snapshots exclude warm-up
by subtraction and also record pending packets. These add measurement overhead;
do not directly compare their CPU to earlier uninstrumented runs. Counters are
sampled across queues at boundaries, not a transactional per-packet accounting
system. The snapshotter retains the initial queue references before measurement
so disconnected peers cannot disappear from cumulative counters. Closed queues
are also counted; this fixed set is for the no-churn matrix, not membership churn.
They help locate dominant loss but cannot uniquely attribute every missing
delivery. Product builds use fixed sixteen-slot queues with no new counters;
the matrix explicitly overrides capacity to four or sixteen for comparison.

Interpret allocation rate, CPU (one logical core = 100%), delivery counts and
latency together. The probe includes real Noise/UDP, JetStream and SQLite, but
no Opus, audio hardware, actual chat publications or game workload. Results on
one shared macOS host are not Linux deployment capacity. See the parent README
for counter, profiler, histogram and loss-attribution limitations. Raw results
and reports stay under ignored `build/`; never deploy the overlay binary.

## Linux Batch I/O

`batch` uses the same rendezvous and completion ownership as `writer`, but drains
currently ready submissions, up to 32 datagrams, before calling
`golang.org/x/net/ipv4.WriteBatch`. There is no timer to wait for a fuller batch.
Queue capacity per member stays at 16; the 32-message syscall limit is separate
from that voice queue. Ciphertext, addresses and message descriptors remain
borrowed until submission completes. Partial writes advance only the successful
prefix, errors fail the unsent suffix, and zero progress terminates the attempt.
Datagrams retain their individual encryption, nonce, destination and boundaries.

The evaluated dependency is `golang.org/x/net v0.56.0`, pinned in an artifact-only
modfile. Its Linux `ipv4/batch.go` path calls `internal/socket.SendMsgs`, which uses
`sendmmsg`; other supported platforms fall back to one message per call. Thus a
macOS call to this API is not evidence of batched system calls. No dependency is
added to the product module. `batch-unit.test` verifies multiple destinations,
partial completion, borrowed buffers, admission timeout and cancellation.

Build all three Linux binaries on the host before measuring:

```sh
sh test/server-perf/socketwriter/build-batch.sh build/server-writer/my-batch
# Use RESONA_BENCH_GOARCH=amd64 for an x86 Linux engine (default: arm64).
docker run --rm --name resona-batch-measure --network none \
  --cpuset-cpus 0-3 --memory 512m --pids-limit 256 --read-only \
  --tmpfs /tmp:rw,nosuid,size=256m --cap-drop ALL \
  --security-opt no-new-privileges --user "$(id -u):$(id -g)" \
  -v "$PWD/build/server-writer/my-batch:/work" \
  --entrypoint /bin/sh golang:1.25 /work/measure.sh
```

The image is only a runtime for statically linked test binaries, not the build
toolchain. Choose valid CPU IDs for your engine; this example uses four CPUs
without a CFS bandwidth quota. No host port, real service data or Docker socket
is mounted. The runner lowers process priority, alternates three models in
reversed pairs, and runs each twice at 4 members/2 speakers, 64/8 and 64/16.
Each interval is 30 seconds after warm-up; override `RESONA_GC_SECONDS` if needed.
Cgroup CPU counters are retained before/after each run to detect quota throttling.

Set `-e RESONA_WRITER_LEVELS=stress32` on the container to add only the
64-member/32-speaker case using the same binaries. The target is 1,600 source
packets/s and 100,800 forwarded packets/s. Results use the `stress32-` prefix;
existing run directories are never replaced. Supported levels are `small`,
`stress8`, `stress16`, and `stress32`; the original three remain the default.

`socket_writer` records measurement-window batch-size counts, WriteBatch calls,
completed datagrams and errors. Compute datagrams/call and the fraction of batches
larger than one before interpreting syscall savings. These diagnostic atomics
exist only in the batch prototype and add some overhead. Confirm `sendmmsg` in a
separate syscall-traced correctness run, never during timed performance runs.

All three queue-instrumented models also count full Noise receive-queue drops.
`server_transport_voice_dropped` measures the child server's inbound drops;
`client_transport_voice_dropped` measures the generator clients' inbound drops.
The existing four-packet transport receive queues are unchanged. Their capacity
is separate from the sixteen-slot outbound VoiceRings. A more bursty batch can
increase receive-side drops even when every server socket submission succeeds.
These counters only count a queue rejection, not all possible kernel, replay,
membership or measurement-boundary losses.
