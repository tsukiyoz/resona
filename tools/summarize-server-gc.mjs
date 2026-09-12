import fs from 'node:fs';
import path from 'node:path';

const root = process.argv[2];
if (!root) throw new Error('Usage: node tools/summarize-server-gc.mjs <artifact-directory>');
const rows = [];
for (const name of fs.readdirSync(root).sort()) {
  const file = path.join(root, name, 'result.json');
  if (!fs.existsSync(file)) continue;
  const r = JSON.parse(fs.readFileSync(file, 'utf8'));
  const windowFile = path.join(root, name, 'windows.jsonl');
  const windows = fs.existsSync(windowFile)
    ? fs.readFileSync(windowFile, 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse)
    : [];
  const count = predicate => windows.filter(predicate).length;
  rows.push({
    name, gogc: r.gogc, members: r.members, rooms: r.rooms,
    seconds: r.seconds, diagnostic: r.diagnostic, churn: r.churn_reconnects,
    cpu_percent: r.cpu_percent_one_core, alloc_mib_s: r.alloc_mib_per_second,
    heap_end_mib: r.heap_end_mib,
    heap_sample_max_mib: windows.reduce((n, w) => Math.max(n, w.heap_mib), 0),
    gc_cycles: r.gc_cycles, pause_total_ms: r.gc_pause_total_ms,
    pause_p99_upper_ms: r.gc_single_pause_p99_bucket_upper_ms ?? 0,
    pause_max_upper_ms: r.gc_single_pause_max_bucket_upper_ms ?? null,
    assist_ms: r.gc_assist_ms, gc_cpu_ms: r.gc_cpu_ms,
    forward: r.forward, end_to_end: r.end_to_end,
    stable_expected: r.expected_stable_deliveries ?? r.expected_without_churn,
    stable_received: r.received_stable_deliveries ?? r.received_voice,
    windows: windows.length,
    windows_with_gc_completion: count(w => w.gc_cycles > 0),
    windows_over20ms_with_gc_completion: count(w => w.max_forward_ms > 20 && w.gc_cycles > 0),
    windows_over20ms_without_gc_completion: count(w => w.max_forward_ms > 20 && w.gc_cycles === 0),
  });
}
fs.writeFileSync(path.join(root, 'summary.json'), `${JSON.stringify(rows, null, 2)}\n`);
console.log(JSON.stringify(rows, null, 2));
