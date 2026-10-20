#!/usr/bin/env python3
"""Prepare recorded-noise screening inputs and a joint, ANS-only blind pack."""

import argparse
import array
import csv
import hashlib
import json
import math
from pathlib import Path
import random
import shutil
import statistics
import subprocess
import sys
import tarfile

RATE = 48000
REVISION = "fe61c4ba0d9ac8dd7e23d719cc79f8947e1dc742"
NOISES = {
    "Typing_1.wav": ("47980660888977fb45e5cdfd4a14d6585139e90267c0e61a01eeadb4a5891222", "b7f9c3d54ab3baebbb2ee6a9ea77228832a2bb3d"),
    "AirConditioner_1.wav": ("3fe55d7035d7ef2599841738f818dc4463f5ef983eb4b5fefeee153d8caa2110", "55e799699ac13da8373c85ada7dc71af7c7b6615"),
    "Babble_1.wav": ("d7a64dd995a92f4790308dbff196332585eec502734ac737bfc9e2ac317dacd6", "7d51348412f879c267b4a40614328321420ad2da"),
}
ARCHIVE_HASH = "c6a87fb4acc7aeb966b12c1f4fd0d3d467caa62476d217e1a662cdf181968a9e"
ATTRIBUTION = """# Round 2 input sources

Chinese speech: Dong Wang, Xuewei Zhang, Zhiyong Zhang, CSLT / Tsinghua
University, THCHS-30 (2015), https://www.openslr.org/18/.
Source archive: https://openslr.magicdatatech.com/resources/18/data_thchs30.tgz.
OpenSLR declares Apache License 2.0: https://www.apache.org/licenses/LICENSE-2.0.
Only the first 2,000,000 archive bytes are fetched; four complete members are
read from that verified prefix, without unpacking incomplete members or links.
The first five seconds of C31_736, B8_404, A23_150 and C6_639 are used.
These are recorded Mandarin readings, not TTS, gaming conversations, or studio
noise-free stems. Original room/microphone noise may remain in the references.
C21_517 was excluded after a source check: a 40Hz high-pass reduced its energy
by 4.10dB, versus 0.11dB for C31_736. Strong infrasonic content made waveform
projection and derived gain matching misleading for high-pass processors.
The selected speech itself is NOT high-pass filtered during preparation.

Recorded noises: Microsoft MS-SNSD, noise_test/Typing_1.wav,
AirConditioner_1.wav and Babble_1.wav, revision
fe61c4ba0d9ac8dd7e23d719cc79f8947e1dc742.
https://github.com/microsoft/MS-SNSD
The dataset README states that its Freesound noises were selected under CC0:
https://creativecommons.org/publicdomain/zero/1.0/.
Reddy et al., A Scalable Noisy Speech Dataset and Online Subjective Test
Framework, Interspeech 2019, pp. 1816-1820.
The locked GitHub content may be fetched via a jsDelivr mirror; SHA-256 and
official Git blob SHA-1 are checked. This is not evidence of exclusion from
any candidate model's training data.

Changes: speech/noise explicitly resampled from 16k to 48k mono float32, speech
trimmed to five seconds with 10ms fades, peak-normalized to 0.3, repeated three
times with digital pauses. Only the first 20s of each noise recording is used;
noise peak-normalized to 0.9 to handle resampling overshoot without clipping.
The evaluator mixes speech/noise at the declared SNR with common headroom.
Level-step and soft cases are amplitude simulations, NOT natural whispering
or shouting. AirConditioner is HVAC, NOT a specific gaming-PC fan. Babble is
competing speech, which an ordinary denoiser need not separate from the user.
Upsampling does not restore full-band content. No product default is selected.
"""


def save_json(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n", encoding="utf-8")


def fetch(path, url, sha256, prefix=False):
    if not path.exists() or hashlib.sha256(path.read_bytes()).hexdigest() != sha256:
        part = path.with_suffix(path.suffix + ".part")
        command = ["curl", "-fLsS", "--retry", "1", "--max-time", "120"]
        if prefix:
            command += ["--range", "0-1999999", "--max-filesize", "2000000"]
        subprocess.run(command + ["-o", str(part), url], check=True)
        if hashlib.sha256(part.read_bytes()).hexdigest() != sha256:
            raise ValueError(f"source checksum mismatch: {path}")
        part.replace(path)


def read_audio(path, seconds):
    result = subprocess.run([
        "ffmpeg", "-v", "error", "-i", str(path), "-t", str(seconds),
        "-ar", str(RATE), "-ac", "1", "-f", "f32le", "pipe:1",
    ], check=True, stdout=subprocess.PIPE)
    samples = array.array("f")
    samples.frombytes(result.stdout)
    if sys.byteorder != "little":
        samples.byteswap()
    if len(samples) != seconds * RATE or any(not math.isfinite(x) for x in samples):
        raise ValueError(f"need {seconds}s of finite audio: {path}")
    return samples


def write_audio(path, samples):
    values = array.array("f", samples)
    if sys.byteorder != "little":
        values.byteswap()
    subprocess.run([
        "ffmpeg", "-v", "error", "-y", "-f", "f32le", "-ar", str(RATE),
        "-ac", "1", "-i", "pipe:0", "-map_metadata", "-1",
        "-c:a", "pcm_f32le", str(path),
    ], input=values.tobytes(), check=True)


def stitch(clip, levels):
    if len(clip) != 5 * RATE or len(levels) != 3:
        raise ValueError("expected a five-second utterance and three levels")
    peak = max(abs(x) for x in clip)
    if peak < 1e-5:
        raise ValueError("speech reference is silent")
    result = array.array("f", [0.0]) * (20 * RATE)
    for start, level in zip([2, 8, 14], levels):
        for i, x in enumerate(clip):
            fade = min(1.0, i / 480.0, (len(clip) - 1 - i) / 480.0)
            result[start * RATE + i] = x * (0.3 / peak) * level * fade
    return result


def prepare(out):
    if (out / "manifest.json").exists():
        raise ValueError("manifest already exists; choose a new input directory")
    source = out / "source"
    source.mkdir(parents=True, exist_ok=True)
    archive = source / "thchs30-prefix.tgz"
    fetch(archive, "https://openslr.magicdatatech.com/resources/18/data_thchs30.tgz", ARCHIVE_HASH, prefix=True)
    wanted = {f"data_thchs30/train/{name}.wav" for name in ["C31_736", "B8_404", "A23_150", "C6_639"]}
    with tarfile.open(archive, mode="r|gz") as contents:
        for entry in contents:
            if entry.name in wanted:
                if not entry.isfile():
                    raise ValueError("expected a regular WAV archive member")
                with contents.extractfile(entry) as incoming, (source / Path(entry.name).name).open("wb") as target:
                    shutil.copyfileobj(incoming, target)
                wanted.remove(entry.name)
                if not wanted:
                    break
    if wanted:
        raise ValueError(f"missing complete archive members: {wanted}")
    for name, (sha256, blob) in NOISES.items():
        fetch(source / name, f"https://cdn.jsdelivr.net/gh/microsoft/MS-SNSD@{REVISION}/noise_test/{name}", sha256)
        data = (source / name).read_bytes()
        actual = hashlib.sha1(f"blob {len(data)}\0".encode() + data).hexdigest()
        if actual != blob:
            raise ValueError(f"official Git blob mismatch: {name}")
        noise = read_audio(source / name, 20)
        peak = max(abs(x) for x in noise)
        if peak < 1e-6:
            raise ValueError(f"silent noise source: {name}")
        write_audio(out / name, (x * (0.9 / peak) for x in noise))
    scenes = []
    definitions = [
        ("01-cn-keyboard-5db", "C31_736", "Typing_1.wav", 5, [1, 1, 1]),
        ("02-cn-hvac-5db", "B8_404", "AirConditioner_1.wav", 5, [1, 1, 1]),
        ("03-cn-babble-5db", "A23_150", "Babble_1.wav", 5, [1, 1, 1]),
        ("04-cn-level-steps", "C31_736", "Typing_1.wav", 10, [0.25, 1, 2]),
        ("05-cn-soft-hvac-0db", "C6_639", "AirConditioner_1.wav", 0, [0.12, 0.12, 0.12]),
    ]
    for name, speaker, noise, snr, levels in definitions:
        write_audio(out / f"{name}-clean.wav", stitch(read_audio(source / f"{speaker}.wav", 5), levels))
        scenes.append({
            "name": name, "clean": f"{name}-clean.wav", "noise": noise,
            "provenance": f"THCHS-30 {speaker}, MS-SNSD {noise}; simulated speech levels {levels}; see ATTRIBUTION.md.",
            "snr_db": snr, "speech_ms": [[0, 5000], [6000, 11000], [12000, 17000]],
        })
    (out / "ATTRIBUTION.md").write_text(ATTRIBUTION, encoding="utf-8")
    save_json(out / "sources.json", {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(source.iterdir()) if p.is_file() and not p.name.endswith(".part")})
    save_json(out / "manifest.json", {"version": 1, "seed": 9812047, "warmup_ms": 2000, "duration_ms": 18000, "repeats": 3, "scenes": scenes})
    print(out / "manifest.json")


def match_candidates(candidates, reference):
    matched = []
    for item, samples in candidates:
        projection = item["quality"]["speech_projection_gain_db"]
        gain_db = max(-12.0, min(12.0, -projection)) if projection is not None else 0.0
        gain = 10 ** (gain_db / 20.0)
        matched.append((item, array.array("f", (x * gain for x in samples)), gain_db))
    peak = max([max(abs(x) for x in reference)] + [max(abs(x) for x in samples) for _, samples, _ in matched])
    headroom = min(1.0, 0.95 / peak) if peak else 1.0
    return [(item, array.array("f", (x * headroom for x in samples)), gain_db + 20 * math.log10(headroom)) for item, samples, gain_db in matched], array.array("f", (x * headroom for x in reference))


def load_runs(regular, little):
    metadata = [json.loads((p / "metadata.json").read_text()) for p in [regular, little]]
    if metadata[0]["inputs"] != metadata[1]["inputs"] or metadata[0]["manifest"] != metadata[1]["manifest"]:
        raise ValueError("regular/little inputs or manifests differ")
    if metadata[0]["rnnoise_model"] != "regular" or metadata[1]["rnnoise_model"] != "little":
        raise ValueError("expected regular, then little model reports")
    rows = [json.loads((p / "answer-key.json").read_text()) for p in [regular, little]]
    selections = [(regular, r) for r in rows[0] if r["case"].endswith("-off")]
    selections += [(little, r) for r in rows[1] if r["case"] == "off-rnnoise-off"]
    return metadata[0], selections


def region_gain(clean, output, start, end, lag):
    xx = sum(x*x for x in clean[start:end])
    xy = sum(clean[i]*output[i+lag] for i in range(start, min(end, len(output)-lag)))
    return 20*math.log10(xy/xx) if xx > 1e-12 and xy > 0 else None


def summarize(regular, little, out):
    metadata, selections = load_runs(regular, little)
    seconds = metadata["manifest"]["duration_ms"] / 1000
    results = []
    for scene in metadata["manifest"]["scenes"]:
        name = scene["name"]
        clean = read_audio(regular / "raw" / f"{name}-clean.wav", seconds)
        for directory, row in selections:
            if row["scene"] != name:
                continue
            output = read_audio(directory / "raw" / f"{name}-{row['case']}.wav", seconds)
            q = row["quality"]
            metrics = row["performance"]
            gains = [region_gain(clean, output, a*48, b*48, q["alignment_samples"]) for a,b in scene["speech_ms"]]
            identity = row["case"] + ("-" + row["model"] if row["case"] == "off-rnnoise-off" else "")
            results.append({
                "scene": name, "implementation": identity,
                "si_sdr_improvement_db": q["si_sdr_improvement_db"],
                "silence_reduction_db": q["silence_reduction_db"],
                "speech_gain_db": q["speech_projection_gain_db"],
                "utterance_projection_gain_db": gains,
                "clean_only_si_sdr_db": row["clean_only_quality"]["aligned_si_sdr_db"],
                "cpu_percent_one_core": statistics.median(p["cpu_percent_one_core"] for p in metrics),
                "mean_us": statistics.median(p["mean_us"] for p in metrics),
                "p99_us": statistics.median(p["p99_us"] for p in metrics),
                "clipped_samples": metrics[0]["clipped_samples"],
            })
    if out.exists():
        raise ValueError("summary directory already exists")
    out.mkdir(parents=True)
    save_json(out / "summary.json", results)
    lines = ["# Recorded-noise round 2 measurements", "",
        "Four THCHS-30 Mandarin speakers, recorded keyboard/HVAC/babble, two amplitude stress cases. ANS only, no AGC. Three timing repeats; CPU normalized to one real-time core. SI-SDR/projection are waveform proxies, not perceptual scores. Pause suppression hits the -120dBFS floor. Original references can contain room noise. No human or game-frame verdict is claimed.", "",
        "Per-utterance gain checks expose whether a loud segment hides suppression of a quieter one. Values are correlation projection estimates, not word-loss detections. Null means no positive projection, not perfect quality. For level-steps the three input levels are -12/0/+6dB. Soft is an amplitude simulation, not natural whispering.", "",
        "| Scene | Candidate | SI-SDR gain dB | Pause reduction dB | Utterance gains dB | CPU % | Mean us | p99 us |", "|---|---|---:|---:|---|---:|---:|---:|"]
    def number(value):
        return f"{value:.2f}" if value is not None else "n/a"
    for row in results:
        gains = "/".join(number(x) for x in row["utterance_projection_gain_db"])
        lines.append(f"| {row['scene']} | {row['implementation']} | {number(row['si_sdr_improvement_db'])} | {number(row['silence_reduction_db'])} | {gains} | {row['cpu_percent_one_core']:.3f} | {row['mean_us']:.1f} | {row['p99_us']:.1f} |")
    (out / "report.md").write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(out / "report.md")


def pack(regular, little, out):
    metadata, selections = load_runs(regular, little)
    seconds = metadata["manifest"]["duration_ms"] / 1000
    if int(seconds) != seconds:
        raise ValueError("round2 pack requires whole-second duration")
    seconds = int(seconds)
    out.mkdir(parents=True, exist_ok=False)
    listening = out / "listening"
    listening.mkdir()
    shutil.copyfile(regular / "listening/SOURCES.md", listening / "SOURCES.md")
    key = []
    rng = random.Random(202610201)
    with (listening / "scores.csv").open("w", newline="", encoding="utf-8") as score_file:
        scores = csv.writer(score_file)
        scores.writerow(["scene", "sample", "speech_clarity_1_to_5", "background_quietness_1_to_5", "naturalness_1_to_5", "notes"])
        for scene in metadata["manifest"]["scenes"]:
            name = scene["name"]
            directory = listening / name
            directory.mkdir()
            selected = [(r, read_audio(p / "raw" / f"{name}-{r['case']}.wav", seconds)) for p, r in selections if r["scene"] == name]
            if len(selected) != 6:
                raise ValueError(f"expected six ANS-only candidates: {name}")
            reference = read_audio(regular / "raw" / f"{name}-clean.wav", seconds)
            matched, reference = match_candidates(selected, reference)
            rng.shuffle(matched)
            write_audio(directory / "reference.wav", reference)
            for label, (item, samples, gain) in zip("ABCDEF", matched):
                write_audio(directory / f"{label}.wav", samples)
                scores.writerow([name, label, "", "", "", ""])
                key.append({"scene": name, "sample": label, "case": item["case"], "model": item["model"], "gain_db": gain})
    save_json(out / "answer-key.json", key)
    save_json(out / "inputs.json", metadata)
    (listening / "README.md").write_text("""# Round 2 blind listening

Listen within one scenario: reference.wav first, then A-F at a fixed player
volume. All six candidates are ANS-only; no AGC is included. Letters are newly
shuffled per scenario, so A does not identify the same engine in every folder.
Speech projection gain is matched within +/-12dB, followed by shared headroom
across BOTH RNNoise models and all controls. Quiet/loud segments within each file
retain their dynamics. Severely suppressed speech may remain quieter due to the
gain limit. Compare quiet pauses, natural voice, consonants/tails and artifacts.
Do not increase volume for a nearly muted sample. Record observations in
scores.csv before viewing the answer key in the parent directory. Keep SOURCES.md
when sharing. These are controlled recordings, not a live/network test.

01: Mandarin + real keyboard, 5dB SNR.
02: different Mandarin speaker + recorded HVAC, 5dB SNR.
03: third Mandarin speaker + recorded background conversation, 5dB SNR.
04: same utterance at -12/0/+6dB, keyboard, 10dB aggregate SNR (not natural shouting).
05: fourth Mandarin speaker, -18.4dB input, HVAC, 0dB SNR (not natural whispering).

The speech/noise sources are 16k resampled to 48k, not full-band gaming captures.
Reference recordings may contain original room noise. No claim about model
training-set exclusion or final product defaults follows from these examples.
""", encoding="utf-8")
    print(listening)


def self_test():
    clip = array.array("f", [0.2]) * (5 * RATE)
    result = stitch(clip, [0.25, 1, 2])
    assert len(result) == 20 * RATE
    for start, end in [(0, 2), (7, 8), (13, 14), (19, 20)]:
        assert not any(result[start * RATE:end * RATE])
    assert abs(result[3 * RATE] - 0.075) < 1e-6
    assert abs(result[9 * RATE] - 0.3) < 1e-6
    assert abs(result[15 * RATE] - 0.6) < 1e-6
    ref = array.array("f", [0.3, -0.3])
    item = {"quality": {"speech_projection_gain_db": -6.020599913}}
    matched, adjusted = match_candidates([(item, array.array("f", [0.15, -0.15]))], ref)
    assert max(abs(x-y) for x,y in zip(matched[0][1], adjusted)) < 1e-6
    muted = {"quality": {"speech_projection_gain_db": None}}
    matched, _ = match_candidates([(muted, array.array("f", [0, 0]))], ref)
    assert not any(matched[0][1])
    assert abs(region_gain(ref, array.array("f", [0.15, -0.15]), 0, 2, 0) + 6.020599913) < 1e-6
    assert region_gain(ref, array.array("f", [0, 0]), 0, 2, 0) is None
    print("round2 preparation and cross-model level-matching checks passed")


def chain_summary(regular, little, out):
    runs = [json.loads((p / "metadata.json").read_text()) for p in [regular, little]]
    if runs[0]["input"] != runs[1]["input"] or runs[0]["bitrates"] != runs[1]["bitrates"]:
        raise ValueError("chain inputs or bitrates differ")
    if [r["rnnoise_model"] for r in runs] != ["regular", "little"]:
        raise ValueError("expected regular then little chain reports")
    rows = [json.loads((p / "results.json").read_text()) for p in [regular, little]]
    if any(not r for r in rows):
        raise ValueError("chain measurements are empty")
    out.mkdir(parents=True, exist_ok=False)
    listening = out / "listening"
    listening.mkdir()
    shutil.copyfile(regular / "SOURCES.md", listening / "SOURCES.md")
    summary = []
    lines = ["# ANS -> gopus -> per-peer AGC2 chain", "",
        "48k mono / 20ms. Independent sender/receiver states, production gopus v0.1.1 and VoIP complexity 5, native ANS and AGC2 configurations. No AEC, devices, network, jitter/PLC, mixing, manual gain or final limiter. CPU includes validation/stage-copy/timing overhead and is normalized to one real-time core; not gaming frame impact. Go allocation counts exclude native allocations. Region RMS includes residual noise; it is not isolated speech gain or perceptual quality. Pause regions exclude 100ms around edges, without waveform alignment. Raw first-repeat outputs retain actual gain; playback uses only common scene headroom, without per-file loudness matching.", "",
        "| Scene | Rate | ANS | CPU % | p99 us | Go allocs/packet | Payload kbps | Pause decoded/AGC2 dBFS | Region gain after AGC2 dB | Full-scale samples |",
        "|---|---:|---|---:|---:|---:|---:|---|---:|---:|"]
    for model, results in zip(["regular", "little"], rows):
        keys = sorted({(r["scene"], r["bitrate"], r["ans"]) for r in results if model == "regular" or r["ans"] == "rnnoise"})
        for scene, bitrate, ns in keys:
            group = [r for r in results if (r["scene"], r["bitrate"], r["ans"]) == (scene, bitrate, ns)]
            if len(group) != runs[0]["input"]["repeats"]:
                raise ValueError("unexpected timing repeat count")
            first = group[0]
            name = f"{ns}-{model}" if ns == "rnnoise" else ns
            row = {"scene": scene, "bitrate": bitrate, "ans": name,
                **{field: statistics.median(r[field] for r in group) for field in ["cpu_percent_one_core", "p99_us", "mean_us", "go_allocs_per_packet", "payload_kbps"]},
                "decoded_levels": first["decoded_levels"], "received_levels": first["received_levels"], "ans_levels": first["ans_levels"]}
            summary.append(row)
            before, after = first["decoded_levels"]["pause_dbfs"], first["received_levels"]["pause_dbfs"]
            noise = f"{before:.1f}/{after:.1f}" if before is not None and after is not None else "n/a"
            gain = first["received_levels"]["speech_dbfs"] - first["decoded_levels"]["speech_dbfs"]
            lines.append(f"| {scene} | {bitrate} | {name} | {row['cpu_percent_one_core']:.3f} | {row['p99_us']:.1f} | {row['go_allocs_per_packet']:.2f} | {row['payload_kbps']:.2f} | {noise} | {gain:.2f} | {first['received_levels']['full_scale_samples']} |")
    for scene in runs[0]["input"]["scenes"]:
        name = scene["name"]
        seconds = runs[0]["input"]["duration_ms"] / 1000
        choices = []
        for rate in runs[0]["bitrates"]:
            choices.extend([(rate, "00-reference.wav", regular / "raw" / f"{name}-clean.wav"),
                            (rate, "01-input.wav", regular / "raw" / f"{name}-input.wav"),
                            (rate, "02-speex-opus-agc2.wav", regular / "raw" / f"{name}-speex-{rate}-agc2.wav"),
                            (rate, "03-off-opus-agc2.wav", regular / "raw" / f"{name}-off-{rate}-agc2.wav")])
            for directory, model, start in [(little, "little", 4), (regular, "regular", 7)]:
                for i, stage in enumerate(["ans", "decoded", "agc2"]):
                    label = ["ans", "opus", "opus-agc2"][i]
                    choices.append((rate, f"{start+i:02}-{model}-{label}.wav", directory / "raw" / f"{name}-rnnoise-{rate}-{stage}.wav"))
        audio = [(rate, label, read_audio(path, seconds)) for rate, label, path in choices]
        peak = max(abs(x) for _, _, samples in audio for x in samples)
        headroom = min(1.0, .95 / peak) if peak else 1.0
        for rate, label, samples in audio:
            directory = listening / name / str(rate)
            directory.mkdir(parents=True, exist_ok=True)
            write_audio(directory / label, (x * headroom for x in samples))
    save_json(out / "summary.json", summary)
    (out / "report.md").write_text("\n".join(lines) + "\n", encoding="utf-8")
    (listening / "README.md").write_text("""# Product codec-chain listening

Each scenario contains 32000/48000 bitrate folders (or the requested rates).
00 is the reference, 01 noisy input, 02 current Speex + codec + AGC2 control,
03 no ANS + codec + AGC2. Compare 04 -> 05 -> 06 for RNNoise little, and
07 -> 08 -> 09 for regular: ANS output, Opus roundtrip, then receiver AGC2.

Use fixed player volume, initially low. AGC2 intentionally changes volume:
these files are NOT individually loudness matched. One shared scene headroom
factor covers both rates and models; raw output remains in the reports. Listen
for consonants/tails, speech-edge artifacts, background returning after AGC2,
and quiet/loud transitions. Start with keyboard and HVAC at 32000, then compare
background conversation at 32000/48000. This is a named diagnostic comparison,
not another blind ANS ranking. Retain SOURCES.md when sharing.

No live device, network, jitter/PLC, mixing, manual gain or final output limiter
is exercised. Original 16k sources are resampled to 48k. Level steps/soft input
are amplitude simulations. This cannot establish full-band gaming quality or
game-frame impact, and does not change the desktop's available ANS backends.
""", encoding="utf-8")
    print(out / "report.md")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    prep = sub.add_parser("prepare")
    prep.add_argument("directory", type=Path)
    bundle = sub.add_parser("pack")
    bundle.add_argument("regular", type=Path)
    bundle.add_argument("little", type=Path)
    bundle.add_argument("directory", type=Path)
    summary = sub.add_parser("summarize")
    summary.add_argument("regular", type=Path)
    summary.add_argument("little", type=Path)
    summary.add_argument("directory", type=Path)
    sub.add_parser("self-test")
    chain = sub.add_parser("chain-summary")
    chain.add_argument("regular", type=Path)
    chain.add_argument("little", type=Path)
    chain.add_argument("directory", type=Path)
    args = parser.parse_args()
    if args.command == "prepare":
        prepare(args.directory)
    elif args.command == "pack":
        pack(args.regular, args.little, args.directory)
    elif args.command == "summarize":
        summarize(args.regular, args.little, args.directory)
    elif args.command == "chain-summary":
        chain_summary(args.regular, args.little, args.directory)
    else:
        self_test()


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, subprocess.CalledProcessError) as exc:
        sys.exit(str(exc))
