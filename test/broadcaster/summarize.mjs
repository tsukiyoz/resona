import fs from 'node:fs';
import path from 'node:path';

const root = process.argv[2];
if (!root) throw new Error('Usage: node test/broadcaster/summarize.mjs <output-directory>');
const rows = fs.readFileSync(path.join(root, 'results.jsonl'), 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse);
const groups = new Map();
for (const r of rows) {
  const key = `${r.scenario}/${r.model}`;
  if (!groups.has(key)) groups.set(key, []);
  groups.get(key).push(r);
}
const median = xs => {
  if (xs.some(x => x == null)) return null;
  xs = [...xs].sort((a, b) => a - b);
  return (xs[Math.floor((xs.length - 1) / 2)] + xs[Math.floor(xs.length / 2)]) / 2;
};
const format = n => n == null ? '>200ms/none' : n.toFixed(3);
const table = [
  '# Broadcaster Architecture Experiment', '',
  'Medians across rounds. CPU is percent of one core; values may exceed 100%.',
  'P99 is a 100-us histogram upper bound through 200 ms; null samples are not silently discarded.', '',
  '| Scenario | Model | Rounds | CPU % | CPU ns/delivered | Drop % median/max | P99 ms | Worst target delivery % | Input lag max ms |',
  '| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |',
];
const summary = [];
for (const [key, rs] of groups) {
  const r = {
    scenario: rs[0].scenario, model: rs[0].model, rounds: rs.length,
    cpu_percent: median(rs.map(r => r.CPUPercent)),
    elapsed_seconds: median(rs.map(r => r.Seconds)),
    cpu_ns_per_delivered: median(rs.map(r => r.Delivered ? r.CPUSeconds * 1e9 / r.Delivered : null)),
    delivered_per_second: median(rs.map(r => r.Delivered / r.Seconds)),
    drop_percent: median(rs.map(r => r.Dropped / r.Offered * 100)),
    drop_max_percent: Math.max(...rs.map(r => r.Dropped / r.Offered * 100)),
    p99_ms: median(rs.map(r => r.P99MS)), p999_ms: median(rs.map(r => r.P999MS)),
    alloc_mib: median(rs.map(r => r.AllocMiB)),
    rejected_percent: median(rs.map(r => r.Rejected / r.Offered * 100)),
    overwritten_percent: median(rs.map(r => r.Overwritten / r.Offered * 100)),
    expired_percent: median(rs.map(r => r.Expired / r.Offered * 100)),
    start_p99_ms: median(rs.map(r => r.StartP99MS)),
    mean_start_ms: median(rs.map(r => r.MeanStartMS)),
    mean_completion_ms: median(rs.map(r => r.MeanCompletionMS)),
    slow_start_mean_ms: median(rs.map(r => r.Members.find(m => m.ID === r.config.SlowMember)?.MeanStartMS)),
    slow_start_p99_ms: median(rs.map(r => r.Members.find(m => m.ID === r.config.SlowMember)?.StartP99MS)),
    ring_signals: median(rs.map(r => r.RingSignals)),
    cas_retries: median(rs.map(r => r.CASRetries ?? 0)),
    minimum_target_delivery_percent: median(rs.map(r => r.MinDeliveryRatio * 100)),
    input_lag_max_ms: Math.max(...rs.map(r => r.MaxInputLagMS)),
    max_ms: Math.max(...rs.map(r => r.MaxMS)),
    healthy_member_drop_percent: median(rs.map(r => {
      const members = r.Members.filter(m => m.ID !== r.config.SlowMember);
      return 100 * members.reduce((n, m) => n + m.Dropped, 0) / members.reduce((n, m) => n + m.Offered, 0);
    })),
  };
  summary.push(r);
  table.push(`| ${r.scenario} | ${r.model} | ${r.rounds} | ${format(r.cpu_percent)} | ${format(r.cpu_ns_per_delivered)} | ${format(r.drop_percent)}/${format(r.drop_max_percent)} | ${format(r.p99_ms)} | ${format(r.minimum_target_delivery_percent)} | ${format(r.input_lag_max_ms)} |`);
}
table.push('', 'Lower CPU is not a win when delivery or offered-input timing is worse.',
  'Mutex profiles are diagnostic, not scored runs. Mutex profile totals accumulate within a process.',
  'Serial-member mode uses a per-member processing mutex for direct and goroutine-wait; queue workers already serialize members.',
  'All models use the same short per-member statistics mutex; synthetic computation is outside that lock.',
  'Direct and goroutine-wait backpressure producers: check elapsed time and input lag, not just drops.', '',
  '| Scenario | Model | Elapsed s | Delivered/s | Alloc MiB | Healthy member drop % |',
  '| --- | --- | ---: | ---: | ---: | ---: |');
for (const r of summary) {
  table.push(`| ${r.scenario} | ${r.model} | ${format(r.elapsed_seconds)} | ${format(r.delivered_per_second)} | ${format(r.alloc_mib)} | ${format(r.healthy_member_drop_percent)} |`);
}
table.push('');
table.push('| Scenario | Model | Reject new % | Overwrite old % | Expired % | Start age mean ms | Start age P99 ms | Slow member start mean/P99 ms |',
  '| --- | --- | ---: | ---: | ---: | ---: | ---: | --- |');
for (const r of summary) {
  table.push(`| ${r.scenario} | ${r.model} | ${format(r.rejected_percent)} | ${format(r.overwritten_percent)} | ${format(r.expired_percent)} | ${format(r.mean_start_ms)} | ${format(r.start_p99_ms)} | ${format(r.slow_start_mean_ms)}/${format(r.slow_start_p99_ms)} |`);
}
table.push('', 'Age is measured from scheduled input time. Expiry is checked before processing; active work can finish after the deadline.',
  'Ring overwrites the oldest queued arrival, never the active delivery. Signal counts are notifications, not OS wakeups.', '');
fs.writeFileSync(path.join(root, 'summary.json'), JSON.stringify(summary, null, 2) + '\n');
fs.writeFileSync(path.join(root, 'report.md'), table.join('\n'));
console.log(table.join('\n'));
