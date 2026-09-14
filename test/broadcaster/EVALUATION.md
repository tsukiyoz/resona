# Initial evaluation, 2026-09-12

Historical shared-resource experiment, superseded on 2026-09-14 by the room/member
queue models. The old resource mutex and Fanout/Resources parameters no longer
exist in the test implementation. These results must not be applied to the new
models; architecture.drawio also depicts the old experiment.

Apple M2, macOS arm64, Go 1.26.4, GOMAXPROCS=8. Three rounds, two seconds per
model/scenario, model order rotated each round. Fixed offered counts, 32 pending
credits per target, measurements include drain. Full method: [README](README.md).
Existing Resona production code and remote deployment were not changed by this
experiment. The models are compiled only as tests.

## CPU result

Medians, percentage of one CPU core:

| Scenario | Target channel workers | Resource channel workers | Resource batch workers |
| --- | ---: | ---: | ---: |
| Paced, one shared output | 3.62 | 2.22 | 2.25 |
| Burst, one shared output | 44.48 | 28.22 | 21.33 |
| Burst, four independent outputs | 31.19 | 28.00 | 24.75 |
| Slow target, four outputs | 6.82 | 5.02 | 4.87 |

For the shared-output burst, process CPU per successfully delivered message fell
from 2,174 ns to 1,378 ns to 1,045 ns. Batching reduced CPU about 52% relative to
target workers. Changing consumer ownership already captured a substantial part
of the benefit before changing the channel to a batch queue. With paced traffic,
resource-channel and resource-batch costs are close: batching is not universally
better than a channel.

## Delivery and latency limits

Paced and slow-target cases delivered everything in all models. Shared-output
burst drop-rate medians were 0.261%, 0.199%, and 0.005% respectively. The batch
model nevertheless had a worse individual round with 0.545% drops and maximum
input-generator lateness of 18.24 ms. Do not omit that round or claim zero loss.
The fixed schedule catches up when late, which creates extra bursts and exposes
both machine scheduling and model queue limits.

Shared-output burst p99 medians were 4.00, 3.78, and 3.93 ms. Thus the large CPU
reduction is not an equally large p99 reduction. Latency begins at scheduled
input time and includes generator lateness. A quiescent-host rerun and longer
capacity sweeps are needed before selecting production latency budgets.

## Mutex diagnostic

Separate three-second shared-output burst runs, one model per fresh process,
CPU profiling enabled and mutex sampling fraction 1:

| Model | Total mutex-profile delay | Main attribution |
| --- | ---: | --- |
| Target channel workers | 23.48 seconds | Shared output mutex in consume |
| Resource batch workers | 22.02 milliseconds | Batch append and swap |

The totals accumulate wait time across goroutines and are not wall-clock or
per-message delay. Runtime mutex profile does not measure all internal channel
locks or CAS cache-coherence costs. Profiled runs are not included in scoring
medians. They support the explanation that removing competition for one output
resource is useful; they do not prove a universal thousandfold speedup.

## Recommendation

Use resource-owned sending with a bounded batch queue as the leading integration
candidate for shared-output high-fanout loads. Keep a resource-channel version as
the simpler comparison. There is no evidence yet that replacing the remaining
short queue mutex with a custom CAS ring is the highest-value next change.

Before production adoption, verify real output/syscall costs, crypto ownership,
fair scheduling, expiry, channel transitions and cancellation. A slow target
that occupies a shared output resource still delays other targets on that
resource. This experiment does not establish isolation against every slow-target
pattern or a safe server implementation.

Raw scoring artifacts: ignored `build/bin/broadcaster/round1/`; separate profiles:
`build/bin/broadcaster/profile-target/` and `profile-batch/`. The generated scoring
report contains per-scenario medians, maximum drop rates and input-lag maxima.
