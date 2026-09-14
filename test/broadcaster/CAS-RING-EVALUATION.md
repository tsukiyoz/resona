# Mutex and CAS ring buffers, 2026-09-14

Two ring implementations are retained side by side:

- member-ring: fixed message slots, short Mutex, overwrite oldest queued arrival.
- member-cas-ring: fixed atomic-pointer slots, immutable entry publication,
  one-way CAS claim and helping cursors; no Mutex or spinlock in queue core.

Member channel is the control. Six workloads, three balanced rotated rounds,
two seconds of scheduled input per run: all 54 cases passed. macOS arm64,
Go 1.26.4, GOMAXPROCS=8, serial-member processing, no network or profiling.
Artifacts: build/bin/broadcaster/cas-ring-comparison-20260914/.

## CPU and allocation

Median CPU, percent of one core (100% = one core):

| Workload | Member channel | Mutex ring | CAS ring |
| --- | ---: | ---: | ---: |
| 10 members, one speaker | 0.495 | 0.501 | 0.627 |
| 10 members, four speakers | 1.271 | 1.717 | 1.816 |
| 64 members, burst | 45.117 | 46.678 | 48.200 |
| 64 members, heavy computation | 204.209 | 216.325 | 224.440 |
| One slow member | 4.989 | 4.635 | 5.189 |
| Slow member, 20 ms expiry | 4.561 | 4.826 | 5.255 |

Large burst drop medians are 0.557%, 0.477%, 1.444%, respectively. CPU per
successful delivery is 1119 ns, 1161 ns, 1211 ns. Heavy-work channel drops 3.055%
at the median, while both rings have zero median drops; its lower CPU therefore
does not establish greater capacity. Read per-round maxima in the full report.

CAS allocates one immutable entry per accepted publication: about 49.219 MiB
per two-second burst run and 24.612 MiB per heavy run. Mutex ring workload
allocation is near zero; setup memory is excluded for every model. The CAS design
uses Go GC to retain entries held by stale readers, avoiding unsafe slot reuse
and pointer ABA. It is not a zero-allocation or fully preallocated implementation.
No claim that every possible lock-free ring is slower follows from this result.

CAS retry counts (failed admission, slot publication or claims; excluding failed
cursor-help CAS) have medians 2635 in burst and 1468 in heavy runs. Notification
counts differ as well: burst medians 93144 for Mutex, 152303 for CAS. These count
successful capacity-one wake sends, not OS wakeups. The comparison includes
these implementation choices rather than isolating the cost of one instruction.

## Freshness and isolation

| Slow member, expiry disabled | Member channel | Mutex ring | CAS ring |
| --- | ---: | ---: | ---: |
| Mean age at processing start, ms | 62.243 | 37.946 | 37.931 |
| P99 start age, ms | 74.4 | 60.3 | 60.0 |
| Healthy members' drop % | 0 | 0 | 0 |

Both rings discard old queued messages and keep independent member workers.
With the same 20 ms pre-processing expiry applied to all models, the slow
member's mean start age is approximately 19.09 ms for all three. Median ring
overwrites then fall to zero because expiry clears stale work sooner than the
capacity bound. Expiry is a policy improvement independently of CAS versus Mutex.

Age begins at scheduled input time, including generator delay. Delivered-age
statistics exclude discarded messages, so drop reasons must be read alongside
them. Expiry checks precede work; an active operation can finish after MaxAge.
Histograms use 100-us buckets through 200 ms, with exact means and maxima retained.

## CAS algorithm and boundaries

1. Reserve a member credit with CAS. If full, claim the oldest queued entry and
   transfer its credit to the new publication. If all credits are active or held
   by unpublished producers, reject the new item rather than wait.
2. Fully initialize a private entry, then publish its pointer into the tail slot
   with CAS. Only after publication does tail advance. A competing producer can
   help tail advance if the original publisher pauses.
3. A consumer or evicting producer claims an entry with a one-way CAS, then
   advances head. Others help head past an already claimed entry. A claim wins
   once; queued overwrite cannot mutate the active consumer's immutable value.
4. Reuse a ring slot only after its old entry is claimed. Monotonic generations
   distinguish turns; entries are never reset or pooled. uint64 generation
   exhaustion panics instead of silently wrapping.

Publication and claim are the core linearization points. Stale failed CAS retries
imply a competing operation changed shared state; there is no owner whose
reserved-but-unfilled slot must be waited on. This is the intended lock-free
core progress argument, supported by adversarial tests, not a machine-checked
proof or production-readiness certification. Individual callers can still starve.
Evict-old and publish-new are separate operations, not an atomic replacement API.

The whole push/worker path is not claimed lock-free: allocation/GC, common
statistics mutexes, sleep notifications and future I/O remain outside the core.
CAS wake notification uses a nonblocking capacity-one channel send on publication;
Mutex ring additionally detects empty-to-nonempty under its lock. No busy polling
is used while idle. Real packet pooling would require a proven reclamation scheme;
reusing an entry still held by a reader would invalidate this algorithm.

## Validation and decision

Passed go test -race ./test/broadcaster -count=10, including both rings:
capacity including active work, newest retention, FIFO among surviving messages,
concurrent producer/consumer wraparound and overwrite, exact drop accounting,
member-local exclusion, expiration, draining and repeated close. CAS-specific
tests pause a producer before or after publication while other producers wrap
the ring and the consumer continues making progress. The 54 scoring cases and
report generation also passed.

The two requested variants now exist and are measured. Current evidence supports
overwrite/expiry for freshness, but does not establish a CPU advantage for this
CAS implementation. Keep both as experimental baselines. Production server code,
network deployment and Git commits remain unchanged by this task.

Background checked during design: [SCQ paper](https://rusnikola.github.io/files/ringpaper-disc.pdf)
discusses the progress and memory-reclamation difficulties of bounded queues.
This test does not import or port SCQ or a third-party queue dependency; its
publish-before-cursor design and progress tests must be assessed on their own.
