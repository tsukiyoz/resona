# Mutex ring comparison, 2026-09-14

Five models, six serial-member scenarios, five rotated rounds of two seconds.
150 runs passed. macOS arm64, Go 1.26.4, GOMAXPROCS=8; no profiling or network.
Artifacts: build/bin/broadcaster/ring-comparison-20260914/.
This is the mutex-backed ring baseline, not the later CAS implementation.

| Scenario | Member channel CPU % | Mutex ring CPU % |
| --- | ---: | ---: |
| 10 members, one speaker | 0.544 | 0.619 |
| 10 members, four speakers | 1.681 | 1.818 |
| 64 members, burst | 48.323 | 48.634 |
| 64 members, heavy computation | 222.804 | 227.282 |
| Slow member | 4.742 | 4.844 |
| Slow member, 20 ms expiry | 5.037 | 5.199 |

CPU is median percent of one core and includes successful and discarded work.
These data do not establish a CPU reduction from the mutex ring.

Without expiry, the slow member's mean processing-start age falls from 62.729 ms
(channel rejecting new messages) to 37.897 ms (ring discarding queued old arrivals).
With the same 20 ms pre-processing expiry applied to both, means are 19.092 ms
and 19.076 ms. The ring's old-overwrite median is then zero: expiry clears stale
work before the capacity bound requires overwrite in most rounds.

The useful distinction is loss policy and independent member progress. Ring
capacity includes active processing, but only queued items can be overwritten.
No output mutex is held across processing; the queue's short mutex covers
push/pop and notification bookkeeping. This test uses message values rather
than pooled network buffers and does not establish speaker-level fairness.

Processing age starts at scheduled input time and includes generator lateness.
The 100-us histogram now extends through 200 ms; exact means/maxima remain.
Expired messages do not appear in delivered-age statistics. Active processing
can complete after the configured expiry age. Consult raw drop reasons and
per-member counts alongside latency.
