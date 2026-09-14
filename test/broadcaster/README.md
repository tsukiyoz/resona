# Channel broadcaster experiment

Standalone test-only models; no production imports, sockets, audio, encryption,
shared output locks or remote connections. Scoring runs on macOS/Linux; correctness
tests also build on other platforms.

Current results: [lock and CAS ring comparison](CAS-RING-EVALUATION.md).
Earlier mutex-ring results: [ring-buffer comparison](RING-EVALUATION.md).
Previous four-model results: [direct calls and queues](DIRECT-EVALUATION.md).
Earlier two-model results: [room/member evaluation](ROOM-EVALUATION.md).

```sh
go test -race ./test/broadcaster
sh test/broadcaster/run.sh
```

The runner builds first, then runs three rotated rounds of both contracts.
For a focused three-model comparison (about two minutes), use:

```sh
BROADCAST_MODE=serial BROADCAST_MODELS=member-queue,member-ring,member-cas-ring \
BROADCAST_ROUNDS=3 sh test/broadcaster/run.sh
```
Go and Node.js are required. Results go to ignored build/bin/broadcaster/<timestamp>:
results.jsonl, environment.json, summary.json and report.md. Existing results
are never overwritten. Per-member delivery counts and P99 are included in JSONL.

## Model

One room with N members. Speakers are members 0..S-1. Each input goes to every
other member, never its sender. No Fanout or Resources parameters.

| Field | Meaning |
| --- | --- |
| Members | Room membership, 2..64 |
| Speakers | Simultaneous input sources, 1..Members |
| Rate | Inputs per second per speaker |
| Capacity | Pending deliveries per member, including active processing |
| Work | Identical synthetic computation iterations per delivery |
| SlowMember | Member with an extra processing delay; -1 disables |
| SlowDelay | Extra processing duration for that member |
| SerialMember | Require member-local serial processing; false permits independent concurrent calculations |
| MaxAge | Drop before processing if scheduled-input age reaches this threshold; 0 disables |

| Architecture | Queue contents | Worker ownership |
| --- | --- | --- |
| member-queue | One delivery per recipient queue | One worker per member |
| room-queue | One input plus accepted-recipient bitmask | One worker for the room, serial recipient processing |
| direct | No queue; call recipients inline | Each speaker loops serially; different speakers run concurrently |
| goroutine-wait | Spawn a goroutine per accepted recipient | Each speaker waits for its whole broadcast, reusing its WaitGroup |
| member-ring | Short-mutex MPSC ring per member, overwrite oldest queued arrival | One worker per member; pop one value and process outside queue lock |
| member-cas-ring | Atomic slot pointers, immutable entries and CAS claim/helping | One worker per member; producers can claim old entries for eviction |

All producers perform O(N) recipient traversal. Existing models use per-member
CAS admission and reject new deliveries when full. The ring checks/reserves
pending credits under its queue mutex and overwrites the oldest queued arrival
when full. If all capacity is active (possible with Capacity=1), it rejects the
new arrival instead. All models include active processing in pending capacity. This
keeps the pending-work limit equal. Direct calls interleave admission and processing;
WaitGroup waits after launching accepted deliveries. Both backpressure the source.
They still finish all scheduled inputs, potentially taking longer than the nominal
interval. Check elapsed time and input lag alongside drops. The room queue moves actual dispatch to its
worker and reduces channel sends, but does NOT eliminate the producer's O(N)
admission work. Its uint64 mask bounds this experiment to 64 members.

A member queue has Capacity slots. The room queue has Members*Capacity slots:
each queued input owns at least one member credit, so this physical buffer cannot
bypass the same per-member bound. Queues are preallocated; allocation measurements
exclude setup, so they do not compare total resident memory.

The mutex ring has exactly Capacity preallocated slots. Its mutex covers only push,
pop, close and notification bookkeeping; computation and simulated I/O delay
are outside it. An empty-to-nonempty transition attempts a capacity-one wake
notification. The worker sleeps on that channel when idle, takes one message
at a time and does not detach a stale batch. Popped message values are owned by
the consumer and cannot be overwritten. Notifications are not OS wakeup counts.

Oldest means publication order, not timestamp order across independent speakers.
The mutex ring is not lock-free. The CAS ring publishes a complete immutable entry
before advancing tail. Other threads can help lagging head/tail cursors; they
never wait for a reserved slot's payload. A one-way CAS claim arbitrates consumer
versus producer eviction. Slot pointers are preallocated, but each accepted push
allocates an entry retained safely by Go GC. This is not an allocation-free ring.
Publication generations are monotonic uint64 and deliberately cannot wrap.

The CAS version reserves per-member credits before publishing; if all credits
are active or held by paused, unpublished producers, a new push rejects rather
than waits. Eviction and subsequent publication are separate operations. The
core has no Mutex or spinlock; allocation, Go runtime, common stats mutex and
channel-based sleep notification do not inherit a whole-program lock-free claim.
CAS notifications attempt a nonblocking capacity-one send after each publication;
mutex-ring notifications can additionally test empty-to-nonempty under its lock.
Both sleep when idle. Signal counts do not measure operating-system wakeups.

Neither experiment adds a production dependency. Real pooled packet
buffers would require explicit ownership/release rules absent from this value-only
experiment. Overwrite fairness across speakers is not established by this test.

Member state is independent; there is no resource mapping or shared output mutex.
Every scenario runs twice: -stateless uses message-local computation; -serial
requires member-local exclusion. Direct and goroutine-wait use a per-member mutex
around computation and delay in serial mode; queue workers already serialize
members. This is mutual exclusion, not total ordering across speakers.

All models use an identical short statistics mutex per member for checksum and
latency accounting, outside synthetic computation. Its overhead is included;
these results are not directly comparable with earlier room-only measurements.
Member workers may process concurrently; the room worker processes serially.
Slow processing blocks only that member's worker in member-queue, but blocks
the whole room worker in room-queue. Recipient order rotates to reduce fixed-ID
bias. This experiment does not model a nonblocking network reactor.

## Scenarios

| Scenario | Members | Speakers | Rate | Work | Extra delay |
| --- | ---: | ---: | ---: | ---: | --- |
| room-10-one | 10 | 1 | 50 | 64 | none |
| room-10-four | 10 | 4 | 50 | 64 | none |
| room-64-burst | 64 | 32 | 200 | 256 | none |
| room-64-heavy | 64 | 32 | 100 | 4096 | none |
| room-10-slow | 10 | 4 | 200 | 64 | member 9: 2 ms |
| room-10-slow-expiry | 10 | 4 | 200 | 64 | same delay; MaxAge=20 ms for every model |

Every row runs in both processing modes, yielding twelve scenarios; BROADCAST_MODE selects one contract.
All scenarios use Capacity=32. Large-room sources share burst deadlines; small-room
sources stagger. Every run attempts Speakers*Rate*seconds inputs and
Speakers*Rate*seconds*(Members-1) deliveries. Late sources catch up rather than
silently generating fewer inputs. Latency starts at scheduled input time and
includes generator lateness, queueing and processing.

## Interpretation and diagnostics

CPU is process CPU relative to one core, including producers, admission, workers,
statistics and final drain. Compare CPU per delivered message, drops, per-member
service and latency together. Lower CPU caused by dropping work is not a win.
P99/P999 use 100-us buckets up to 200 ms; overflow is null. Exact means, maximum
and input-generator maximum lateness are retained. Processing-start age and
completion age are measured from scheduled input time, not enqueue time. Expiry
is checked after any processing mutex wait and before work, in every model.
Already-active work may finish beyond MaxAge. Expired messages are not included
in delivered-message age statistics; read drop reasons alongside those metrics. These are short synthetic runs,
not production server capacity estimates.

Optional variables: BROADCAST_SECONDS (0.1..10, default 2), BROADCAST_ROUNDS
(1..10, default 3), BROADCAST_SCENARIO, BROADCAST_MODEL (one model),
BROADCAST_MODELS (comma-separated subset, mutually exclusive with BROADCAST_MODEL), BROADCAST_MODE
(serial/stateless; unset runs both), GOMAXPROCS.

```sh
BROADCAST_SECONDS=3 BROADCAST_ROUNDS=1 \
BROADCAST_SCENARIO=room-64-burst-serial BROADCAST_MODEL=room-queue \
BROADCAST_PROFILE=1 sh test/broadcaster/run.sh
```

Use a fresh process and one model/scenario per profile run. CPU and mutex profiles
are diagnostic and must not be pooled into scoring results. Channel synchronization, per-member locks and CAS still cost CPU. Mutex profiles alone cannot measure all of that cost.

The previous shared-resource evaluation in [EVALUATION.md](EVALUATION.md) and
[architecture.drawio](architecture.drawio) are historical, superseded experiments.
Their numbers do not describe these room/member models.
