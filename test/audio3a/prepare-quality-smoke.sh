#!/usr/bin/env bash
set -euo pipefail
# Small public human-speech sanity fixture, not a representative listening corpus.
out=${1:-build/audio3a/quality-input}
command -v ffmpeg >/dev/null
mkdir -p "$out"
source_wav="$out/voices-source.wav"
curl -fLsS --retry 2 --max-time 90 \
    https://download.pytorch.org/torchaudio/tutorial-assets/Lab41-SRI-VOiCES-src-sp0307-ch127535-sg0042.wav \
    -o "$source_wav.part"
expected=c65fcd726d6b08c82c1e5dc7558f863cd8d483e3ed2f4a7bcf271dc1865ada14
if command -v sha256sum >/dev/null; then
    actual=$(sha256sum "$source_wav.part" | cut -d ' ' -f 1)
else
    actual=$(shasum -a 256 "$source_wav.part" | cut -d ' ' -f 1)
fi
test "$actual" = "$expected"
mv "$source_wav.part" "$source_wav"
# Explicit resampling from 16k to 48k does not restore missing high frequencies.
# Two copies separated by silence exercise speech onsets and noise-only pauses.
ffmpeg -v error -y -i "$source_wav" -filter_complex \
    '[0:a]aresample=48000,aformat=channel_layouts=mono,atrim=duration=3.4,asetpts=PTS-STARTPTS,asplit=2[a][b];[a]adelay=1000[aa];[b]adelay=5400[bb];[aa][bb]amix=inputs=2:normalize=0,apad=whole_len=480000,atrim=end_sample=480000[out]' \
    -map '[out]' -c:a pcm_f32le "$out/clean.wav"
ffmpeg -v error -y -f lavfi \
    -i 'anoisesrc=color=pink:amplitude=0.2:seed=20261020:sample_rate=48000:duration=10' \
    -c:a pcm_f32le "$out/noise.wav"
cp test/audio3a/quality-example.json "$out/manifest.json"
cp test/audio3a/QUALITY-SOURCES.md "$out/ATTRIBUTION.md"
ffmpeg -version > "$out/ffmpeg-version.txt"
printf 'Prepared %s/manifest.json\n' "$out"
