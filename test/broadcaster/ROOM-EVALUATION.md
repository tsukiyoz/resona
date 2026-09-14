# Room versus member queues, 2026-09-14

Supersedes the shared-output-resource experiment. No production server changes.
macOS arm64, Go 1.26.4, GOMAXPROCS=8; three rounds of two seconds per scenario/model,
alternating model order, without profiling. All 30 runs passed accounting checks.
Raw local artifacts: build/bin/broadcaster/room-comparison-20260914/.

## Results

CPU is percent of one core; drops are percentages of offered recipient deliveries.
Values below are medians of three rounds, not production capacity estimates.

| Scenario | Member CPU | Room CPU | Member drop % | Room drop % | Member P99 ms | Room P99 ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 10 members, 1 speaker | 0.629 | 0.489 | 0 | 0 | 1.440 | 1.190 |
| 10 members, 4 speakers | 1.939 | 1.376 | 0 | 0 | 1.610 | 0.930 |
| 64 members, 32 speakers, burst | 48.265 | 32.261 | 0.375 | 0.460 | 4.100 | 3.030 |
| 64 members, heavier computation | 212.273 | 100.673 | 0 | 36.006 | overflow | 17.010 |
| 10 members, one slow recipient | 4.886 | 3.965 | 4.319 | 31.944 | overflow | overflow |

Overflow means at least one round's P99 exceeded the 20 ms histogram range;
the summarizer conservatively preserves that overflow. Heavy member-queue runs
had maximum drop 3.946% despite a zero median and maximum input lateness of
105.491 ms. Its additional parallelism does not establish good tail latency.
Normal small-room runs had zero drops in every round.

The room queue reduced CPU by about 29% in the four-speaker small room, and 33%
in the large burst case. Large-burst CPU per delivered message was 1201 ns for
member-queue versus 802 ns for room-queue. Burst drops were nonzero in both;
the room model's median drop was slightly worse.

## Slow-recipient isolation

Member 9 takes an extra 2 ms per delivery. This deliberately exceeds its service
capacity at 800 offered inputs per second and exposes backpressure behavior.

| Metric, median | Member queue | Room queue |
| --- | ---: | ---: |
| Drops among the other nine members | 0% | 31.117% |
| Successful deliveries per second, all members | 6652 | 4731 |
| CPU ns per successful delivery | 7345 | 8376 |

The room model's lower total CPU here is not a win: it completes much less work
and uses more CPU per successful delivery. One blocking recipient delays the
room worker and fills healthy recipients' pending credits. Member queues isolate
that blocking work. This does not claim UDP sends always block per recipient;
it is an explicit simulated processing condition.

## Interpretation

Prefer neither model unconditionally. Room queues reduce queue operations and
worker scheduling for cheap, nonblocking work. Member queues provide independent
progress and parallelism when delivery processing is expensive or blocking.
Both are free of the old artificial output mutex; channel synchronization and
per-member CAS still exist. No lock-wait-duration reduction is claimed without
a separate diagnostic profile.

For a server integration, distinguish room-level routing from per-recipient
encryption/output work before choosing ownership. These experiments do not yet
test a hybrid architecture, network I/O, crypto, channel changes or cancellation.

Fairness: identical per-member pending limits include active work. Both producers
still perform O(N) admission; the room queue holds one input with an accepted-
recipient mask and performs actual dispatch on its worker. It is not a benchmark
of eliminating all producer-side fanout work. Setup memory is excluded from CPU
and allocation scoring. Scheduled-input timestamps include generator lateness.

Validation: go test -race ./test/broadcaster; deterministic capacity/active-work
test, self-exclusion and exact delivery counts, matching payload checksums,
member-worker isolation, idempotent drain; full runner and JSON/Markdown report.
