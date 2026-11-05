# Local server profiling

The opt-in macOS/Linux probe in `internal/server/gcprobe_unix_test.go` runs the real
server in a child process and minimal protocol clients in its parent. It binds
only loopback, creates a temporary JetStream/SQLite store, uses random identities
and never joins an existing server. No public profiling endpoint is needed.

Compile once, then measure without concurrent builds or `-race`:

```sh
mkdir -p build/server-perf
go test -c -o build/server-perf/server.test ./internal/server
GOMAXPROCS=4 RESONA_SERVER_GC_PROBE=1 \
  RESONA_GC_MEMBERS=16 RESONA_GC_ROOMS=1 RESONA_GC_SPEAKERS_PER_ROOM=4 \
  RESONA_GC_SECONDS=30 RESONA_GC_OUTPUT="$PWD/build/server-perf/baseline" \
  build/server-perf/server.test -test.run='^TestServerGCProbe$' -test.v -test.timeout=5m
```

Use a different output directory for each run. Parameters:

| Environment Variable | Default | Meaning |
| --- | --- | --- |
| `RESONA_GC_MEMBERS` | 16 | Total connected members, 4-64 |
| `RESONA_GC_ROOMS` | 1 | Equal-sized channels, 1-4; must divide members |
| `RESONA_GC_SPEAKERS_PER_ROOM` | 4 | Simultaneous speakers in each channel, 1-63; fewer than members per channel |
| `RESONA_GC_SECONDS` | 30 | Measured duration in 50 Hz frames, 1-1800 seconds |
| `RESONA_GC_WARMUP_SECONDS` | 3 | Actual voice traffic before measurement, 0-30 seconds |
| `RESONA_GC_CHURN` | unset | `1` reconnects one listener every two seconds |
| `RESONA_GC_PROFILE` | unset | One of `cpu`, `memory`, `mutex`, `block`, `trace` |
| `RESONA_GC_PERCENT` | 100 | Child GOGC; 400 and off are diagnostics only (off limited to 45 seconds) |

The child uses a 256 MiB soft Go memory limit. Every speaker sends an 80-byte
synthetic payload at 50 Hz (32 kbps), with timestamp fields for measurement. It
is not encoded speech. Speakers are sent in bursts at each tick; this is a
deliberately synchronized workload. All guests remain connected and read control
traffic. The real server automatically starts current-channel history and live
chat consumers, so those broker workers remain present. No additional resource
list interests, chat publications or older history pages are requested.

`result.json` records actual elapsed time, CPU as a percentage of one logical
core, allocation rate, heap, GC pauses, received/expected deliveries and latency
histograms. `forward` starts after server transport reception and ends after
outbound encryption/socket submission; it excludes inbound socket/decryption
wait. `end_to_end` also includes both local transport stacks and the generator's
receive scheduling. Buckets are 10 us wide; percentile values are bucket upper
bounds, and values above 50 ms cannot be resolved (the exact maximum is retained).
No churn loss rate should be computed from `expected_without_churn`; use stable
members' counts instead. Warm-up, admission and shutdown are outside the window.
Baseline runs still include the diagnostic timestamp wrappers, atomic counters
and 100 ms metric sampling; they are not zero-overhead production measurements.
The target source/forward packet rates and achieved source/expected-forward/
receive rates are recorded separately. If the generator falls behind its ticks,
use actual elapsed time and achieved rates rather than assuming the target load
was sustained. Increasing simultaneous speakers stresses the existing sixteen-slot
voice queues; their capacity, freshness rules and timeouts remain unchanged.

Repeat a baseline without profiling before interpreting results. Run each
diagnostic separately using `RESONA_GC_PROFILE` and a fresh output directory:

- `cpu`: CPU sampling during the traffic window, no runtime trace.
- `memory`: cumulative allocation and live heap profiles after the window. The
  allocation profile includes startup/warm-up; live heap follows a forced GC
  outside the measured interval, while `heap_end_mib` is before this forced GC.
- `mutex`: samples all contended lock releases; includes startup and warm-up
  stacks only if sampling was enabled elsewhere (it is normally off).
- `block`: samples blocking with a 1 ms rate; expected idle reads and channel
  waits are included. Aggregate blocked goroutine time is not wall time or CPU.
- `trace`: Go execution trace only; use it after identifying a scheduling issue.

```sh
go tool pprof -top build/server-perf/cpu/cpu.pprof
go tool pprof -http=127.0.0.1:18081 -no_browser build/server-perf/cpu/cpu.pprof
# Open /ui/flamegraph in that local web UI.
go tool pprof -sample_index=alloc_space -top build/server-perf/memory/allocs.pprof
go tool pprof -sample_index=inuse_space -top build/server-perf/memory/heap.pprof
go tool pprof -top build/server-perf/mutex/mutex.pprof
go tool pprof -top build/server-perf/block/block.pprof
```

Server and generator have separate Go heaps/profiles but share the same host CPU
and loopback networking. Record the host OS and container limits. Loopback tests
are not production capacity, Windows audio quality, network RTT or gaming
interference guarantees; Linux inside Docker on macOS also includes VM scheduling.
Raw profiles, traces, logs and generated reports belong under ignored `build/`.

The isolated [single socket writer experiment](socketwriter/README.md) compares
the current socket gate with an owner goroutine using a Go build overlay. It is
not included in product builds and is not a deployable transport option.

For allocation changes, retain separate test binaries from before and after the
change, alternate baseline/optimized runs with identical parameters, and compare
at least two runs without profiling. Do not run compilers or race tests alongside
measurements. Record delivery counts and tail latency alongside allocation rate;
lower allocation alone does not establish lower latency or CPU use.

The synchronous encrypted-send microbenchmark isolates packet allocation from
socket and queue work:

```sh
go test ./internal/noiseudp -run '^$' -bench '^BenchmarkSendDatagram$' -count=3
```

If the local Go installation reports `go: no such tool "pprof"`, build its bundled
official source with `go build -o build/server-perf/pprof cmd/pprof` and substitute
`build/server-perf/pprof` for `go tool pprof`. This does not add a module dependency.
Exporting `/ui/flamegraph` saves inline HTML/JS for offline zooming; links to other
pprof views and source navigation still require the local pprof web server.
