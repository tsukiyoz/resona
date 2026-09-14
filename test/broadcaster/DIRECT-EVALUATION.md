# Direct calls versus queues, 2026-09-14

Four models, five workloads, two processing contracts, four rotated rounds:
160 successful runs. Each case offers two seconds of scheduled inputs; delayed
producers finish the same input count, potentially extending elapsed time.
macOS arm64, Go 1.26.4, GOMAXPROCS=8, no profiling. Raw local artifacts:
build/bin/broadcaster/direct-comparison-20260914/.

No production server code or remote deployment was changed.

## CPU comparison

Median process CPU, percent of one core. Includes admission, processing,
instrumentation, goroutine scheduling and final drain.

| Stateless workload | Member queue | Room queue | Direct loop | Goroutine + WaitGroup |
| --- | ---: | ---: | ---: | ---: |
| 10 members, 1 speaker | 0.68 | 0.52 | 0.48 | 0.81 |
| 10 members, 4 speakers | 1.89 | 1.26 | 1.32 | 2.09 |
| 64 members, burst | 44.97 | 32.94 | 38.83 | 76.75 |
| 64 members, heavy computation | 207.41 | 100.81 | 223.11 | 236.97 |

| Member-serial workload | Member queue | Room queue | Direct loop | Goroutine + WaitGroup |
| --- | ---: | ---: | ---: | ---: |
| 10 members, 1 speaker | 0.60 | 0.47 | 0.44 | 0.72 |
| 10 members, 4 speakers | 1.78 | 1.33 | 1.26 | 1.93 |
| 64 members, burst | 48.54 | 33.67 | 39.96 | 85.46 |
| 64 members, heavy computation | 214.52 | 100.06 | 211.99 | 228.72 |

These CPU values are not rankings without delivery and timing data. In heavy
workloads the room queue drops a median 35.57% (stateless) / 36.49% (serial).
Direct and WaitGroup drop zero in all measured cases, but can accumulate input
lateness. Small-room no-delay workloads have zero drops in all models/rounds.

Large-burst stateless drop medians are 0.818%, 0.603%, 0%, 0%, respectively;
serial medians are 0.128%, 0.916%, 0%, 0%. Thus the direct loop's higher CPU than
the room queue also accompanies higher delivery. CPU per delivery in serial
burst is 1205 ns, 841 ns, 989 ns, 2116 ns, respectively.

## Allocation and latency

WaitGroup reuses one wait group per speaker, yet spawning a goroutine per
delivery allocates about 80 MiB in each two-second large-burst run and 40 MiB
in heavy runs. The other models have near-zero workload allocations; preallocated
queues, member statistics and runtime setup are excluded from this metric.

There is no universal tail-latency winner. Serial burst P99 medians are 4.455 ms
(member), 4.285 ms (room), 3.920 ms (direct); WaitGroup has an overflow round.
For serial heavy work the direct model has an overflow round and maximum input
lateness 88.6 ms, while WaitGroup P99 median is 5.280 ms. This is a counterexample
to claiming the direct loop always has better tails.

Histograms stop at 20 ms. The summarizer conservatively reports null if any
round's quantile overflows; it does not compute a numeric median after discarding
that round. Short host-local runs and scheduler variability limit tail claims.

## Slow member

Member 9 has 2 ms extra processing; four speakers each offer 200 inputs/s.
With required member-local serialization, this overloads that member.

| Serial mode, median | Member queue | Room queue | Direct loop | Goroutine + WaitGroup |
| --- | ---: | ---: | ---: | ---: |
| Total elapsed seconds | 2.071 | 2.071 | 3.663 | 3.687 |
| All-recipient drop % | 4.306 | 32.434 | 0 | 0 |
| Healthy-recipient drop % | 0 | 31.609 | 0 | 0 |
| Successful deliveries/s | 6654 | 4699 | 3931 | 3906 |

Direct and WaitGroup maximum source lateness across rounds reaches 1.779 s and
1.731 s. Zero drops hides substantial producer backlog, not low latency. Member
queues isolate the slow member; room queues let it delay healthy members.

In stateless mode different speakers may process the same member concurrently.
Direct and WaitGroup then finish in about 2.001 s with zero drops; member/room
workers still process serially by architecture. That advantage depends on allowing
concurrent member processing and cannot be applied to stateful output blindly.

## Decision

Keep direct loops as a first-class baseline. For cheap nonblocking processing
they avoid task handoffs, and independent speakers already supply concurrency.
These workloads do not justify per-delivery goroutine creation as a CPU-saving
default. Heavy work can benefit from concurrency but does not require spawning
new goroutines for each delivery. A fixed worker pool has not been evaluated.

Use member queues when independent progress, member-local ownership and bounded
backlogs matter. A room queue is cheaper for some light workloads but serializes
all delivery work. Production selection still requires actual encryption/output
behavior, ordering and cancellation contracts; no server redesign is authorized
by these synthetic results alone.

## Method and validation

Stateless computation uses message-local values. Serial mode requires exclusion
for the complete processing operation: direct/WaitGroup acquire a member mutex;
queue workers already provide exclusion. No shared output resource exists.
An identical short statistics mutex is used in every model outside computation;
its cost is included. Earlier measurements without that instrumentation should
not be numerically compared with this run.

All models retain per-member CAS credits. Direct interleaves admission and
processing; WaitGroup waits for the whole broadcast; queue models let sources
continue asynchronously. This backpressure difference is part of the comparison.
Member-local exclusion does not establish a total order across speakers.

Passed: go test -race ./test/broadcaster, self-exclusion and payload accounting,
active-work capacity bound, member-local exclusion, independent member progress,
drain and idempotent close, all 160 scored cases, report generation and JS syntax.
