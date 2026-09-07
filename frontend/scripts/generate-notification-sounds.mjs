import { mkdir, writeFile } from 'node:fs/promises';

// Original notification tones, generated deterministically without external assets.
const sampleRate = 48000;
const output = new URL('../public/sounds/', import.meta.url);
const sounds = {
  connected: [[392, 0], [523.25, 0.12], [659.25, 0.24]],
  'member-joined': [[523.25, 0], [783.99, 0.14]],
  'member-left': [[659.25, 0], [440, 0.14]],
  disconnected: [[523.25, 0], [392, 0.15], [261.63, 0.30]],
};
await mkdir(output, { recursive: true });
for (const [name, notes] of Object.entries(sounds)) {
  const duration = notes.at(-1)[1] + 0.25;
  const count = Math.ceil(duration * sampleRate);
  const wav = Buffer.alloc(44 + count * 2);
  wav.write('RIFF', 0);
  wav.writeUInt32LE(wav.length - 8, 4);
  wav.write('WAVEfmt ', 8);
  wav.writeUInt32LE(16, 16);
  wav.writeUInt16LE(1, 20);
  wav.writeUInt16LE(1, 22);
  wav.writeUInt32LE(sampleRate, 24);
  wav.writeUInt32LE(sampleRate * 2, 28);
  wav.writeUInt16LE(2, 32);
  wav.writeUInt16LE(16, 34);
  wav.write('data', 36);
  wav.writeUInt32LE(count * 2, 40);
  for (let i = 0; i < count; i++) {
    let sample = 0;
    for (const [frequency, start] of notes) {
      const t = i / sampleRate - start;
      if (t < 0 || t >= 0.24) continue;
      const attack = Math.min(1, t / 0.015);
      const release = Math.min(1, (0.24 - t) / 0.06);
      const envelope = attack * release * Math.exp(-t * 9);
      sample += 0.24 * envelope * (Math.sin(2 * Math.PI * frequency * t)
        + 0.12 * Math.sin(4 * Math.PI * frequency * t));
    }
    wav.writeInt16LE(Math.round(Math.max(-1, Math.min(1, sample)) * 32767), 44 + i * 2);
  }
  await writeFile(new URL(`${name}.wav`, output), wav);
}
