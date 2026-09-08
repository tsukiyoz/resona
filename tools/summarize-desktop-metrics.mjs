import { readFileSync } from 'node:fs';

const [file, ...definitions] = process.argv.slice(2);
if (!file || !definitions.length) throw new Error('usage: node tools/summarize-desktop-metrics.mjs samples.csv name=pid,pid ...');
const [header, ...lines] = readFileSync(file, 'utf8').trim().split('\n');
if (header !== 'monotonic_seconds,pid,cpu_ns,physical_bytes,resident_bytes,idle_wakeups,interrupt_wakeups') throw new Error('unexpected sample schema');
const rows = lines.map(line => line.split(',').map(Number));
if (rows.some(row => row.length !== 7 || row.some(n => !Number.isFinite(n)))) throw new Error('invalid numeric sample');
const grouped = Object.groupBy(rows, row => row[1]);
const result = {};
for (const definition of definitions) {
  const [name, ids] = definition.split('=');
  const pids = ids.split(',').map(Number);
  if (new Set(pids).size !== pids.length) throw new Error('duplicate PID');
  let samples;
  let duration;
  let cpu = 0;
  let wakes = 0;
  for (const pid of pids) {
    const values = grouped[pid];
    if (!values || values.length < 2) throw new Error(`missing PID ${pid}`);
    const elapsed = values.at(-1)[0] - values[0][0];
    if (samples && (samples.length !== values.length || duration !== elapsed)) throw new Error('unequal sampling windows');
    duration = elapsed;
    samples ??= values.map(() => 0);
    values.forEach((row, i) => { samples[i] += row[3] / 1048576; });
    cpu += (values.at(-1)[2] - values[0][2]) / 1e9 / elapsed * 100;
    wakes += (values.at(-1)[6] - values[0][6]) / elapsed;
  }
  result[name] = {
    pids, samples: samples.length, seconds: duration,
    meanPhysicalMiB: samples.reduce((a, b) => a + b, 0) / samples.length,
    minPhysicalMiB: Math.min(...samples), maxPhysicalMiB: Math.max(...samples),
    cpuPercentOneCore: cpu, interruptWakeupsPerSecond: wakes,
  };
}
console.log(JSON.stringify(result, null, 2));
