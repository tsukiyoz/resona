# Audio 3A benchmark

An isolated client-side experiment. It changes neither production audio settings
nor the desktop/server dependencies. AEC, noise suppression (NS/ANS), and AGC are
independent choices in the runner, in that order. Product defaults are not chosen
by this benchmark. SpeexDSP remains the proposed default AEC; actual settings UI
integration and persistence are a separate task.

## Implementations and provenance

| Module | Candidates | Configuration |
| --- | --- | --- |
| AEC | off, SpeexDSP, WebRTC AEC3 | Speex 200 ms filter with residual suppression; AEC3 auto delay, forced HPF |
| NS | off, SpeexDSP, WebRTC, RNNoise, nnnoiseless | Speex -20 dB, WebRTC Moderate, neural default model |
| AGC | off, SpeexDSP, WebRTC AGC2 | Software gain only; maximum +18 dB; no device volume modification |

- SpeexDSP is the repository's existing vendored 1.2.1 source. Speex AGC target
  is 8192 PCM units. Product capture frames are currently 20 ms; this harness uses
  10 ms in the original module/quality runner. The separate codec-chain runner
  below uses the product's 20 ms Speex frame.
- WebRTC APM: `webrtc-audio-processing` and sys/config crates 2.1.0, bundled C++
  repackaging 2.1; wrapper source commit
  `c14d7af1760baff83e8210fee336a0cae0faaa7d`. This is not a pure Rust AEC3 rewrite
  or a claim to use today's Chromium revision. AGC2 adaptive digital is explicitly
  enabled, initial gain 0 dB, headroom 5 dB, input-volume controller disabled.
- nnnoiseless 0.5.2, source `924a2dd143ccad7bce9e5bda061b60ca32911a67`, built-in model.
- Official C RNNoise source `70f1d256acd4b34a572f999a05c87bf00b67730d`, model archive
  SHA-256 `0a8755f8e2d834eff6a54714ecc7d75f9932e845df35f8b59bc52a7cfe6e8b37`.
  Downloaded archive is verified before extraction. Regular and little model
  builds are separate executables, avoiding mutable global model state. x86_64
  builds use upstream runtime SSE4.1/AVX2 dispatch with separately compiled kernels;
  ARM64 uses the compiler's NEON path. No GPU is used.

Cargo.lock pins transitive dependencies. The bundled WebRTC Meson wrap also pins
Abseil source/patch archive hashes. Its upstream Meson default is `debugoptimized`
(-O2 with symbols, WebRTC NDEBUG); our Rust/C adapter builds use Cargo release.
This is a comparison of these implementations/build configurations, not languages.
The two neural candidates have different model generations: their times do not
represent equal output quality. NS levels and AGC target semantics are not equal
across implementations either.

Uniform full-Speex and full-WebRTC presets use a single integrated processor;
mixed presets use separate ordered stages. Thus full-chain cost need not equal
the sum of standalone rows. Speex's AEC row includes preprocessing/residual echo
suppression, and AEC3 enforces a high-pass filter. These are practical processors,
not isolated mathematical kernels. Do not stack two NS engines for comparison.
The 2.1.0 Rust APM wrapper allocates a channel-pointer Vec per capture/render call;
these allocations are included in the timings. They are adapter overhead, not an
intrinsic AEC3/AGC2 requirement. A production zero-allocation adapter needs separate
qualification; this experiment does not silently patch the upstream wrapper.

## GitHub Actions

Actions -> **Audio 3A benchmark** -> Run workflow. Manual only, read-only repository
permissions, no credentials or user audio. Windows x64 and Linux x64 jobs run on
native hosted runners. Artifacts last 14 days:

- `package/`: regular/little executables, this guide, upstream license notices.
- `reports/`: build metadata, dependency inventory/lockfile, CSV/JSON/Markdown,
  failures, per-mode logs, and float32 WAV listening samples.

The regular executable runs 13 presets; little runs RNNoise-only across all scenes.
Each case uses 3 seconds of continuous warmup and 6 measured seconds, three repeats
with rotated case order. The eight scenes are silence, echo-only, noise-only,
speech-noise, double-talk, input-level steps, an echo-path delay change, and clean speech.
The original eight-scene inputs are deterministic harmonic signals with
noise/transients, **not speech**. A separate job now prepares the small human-speech
smoke fixture described below, uploads it with attribution, and gives the exact
same PCM files to both platform jobs. Both model builds also run `quality` and
include its reports/listening outputs. Hosted-runner results remain pipeline
evidence and preliminary screening, not a perceptual or gaming-performance verdict.
Every scene's exact input/reference/clean arrays are hashed. No test server or
microphone is accessed. A hosted-runner OS comparison also compares different CPUs.

Processing failures mark the job red; scripts still attempt the second model and
upload existing reports. A successful job only establishes execution and basic
finite/bounded-output checks, not intelligibility, echo quality or production fitness.

## Run downloaded tools

Windows, from PowerShell in the extracted `package` folder:

```powershell
.\resona-3a-bench.exe --out results --wavs
.\resona-3a-bench.exe --aec speex --ns nnnoiseless --agc agc2 --out mixed --wavs
.\resona-3a-bench-little.exe --aec off --ns rnnoise --agc off --out little --wavs
```

No SDK or microphone permission is required. Linux: `chmod +x resona-3a-bench*`,
then use `./resona-3a-bench` with the same arguments; compatible libc is required
(CI baseline Ubuntu 24.04). See `--help` for duration/repeat limits.

Real speech can be supplied with `--near speech.wav --far remote.wav`. Inputs must
be 48 kHz mono PCM16 or normalized float32 WAV and cover warmup + measured duration.
No implicit resampling or looping occurs. Each source becomes the near/far signal
in the deterministic scenes; background noise and echo paths remain synthetic.

## Real-speech quality screening

Use `quality` for a fixed clean/noise pair with known speech and silence regions,
instead of treating lower output energy as better speech quality:

```sh
# From the repository, requires curl and FFmpeg. Downloads one 109KB public WAV.
bash test/audio3a/prepare-quality-smoke.sh
build/audio3a/package/resona-3a-bench quality \
  build/audio3a/quality-input/manifest.json build/audio3a/quality-regular
build/audio3a/package/resona-3a-bench-little quality \
  build/audio3a/quality-input/manifest.json build/audio3a/quality-little
```

Windows downloaded tools support the same `quality MANIFEST.json NEW_OUTPUT_DIR`
arguments. Prepare inputs with the script on a machine with FFmpeg, then copy the
whole input directory; no FFmpeg, network, audio device or SDK is needed to run
the prepared evaluation on Windows. Output directories must not already exist.
The example manifest and attribution are included in the tool package.

The smoke fixture uses a short **human English** VOiCES recording (CC BY 4.0),
explicitly resampled from 16k to 48k, with repeated utterances and digital pauses,
mixed with **synthetic pink noise** at 5/15dB SNR. See [QUALITY-SOURCES.md](QUALITY-SOURCES.md).
This is a pipeline check and preliminary screen, not a Mandarin/full-band or
gaming-noise qualification. No model-training exclusion is asserted.

For representative tests, copy `quality-example.json` and supply consented local
clean/noise recordings. Paths are relative to the manifest. Both inputs must be
48kHz mono PCM16 or normalized float32, covering warmup plus measured duration;
no resampling, looping or truncation to a shorter file is implicit. `speech_ms`
contains ordered, nonoverlapping ranges **relative to the measured portion**.
The clean reference outside those ranges must be below -60dBFS RMS. Include real
pauses of at least 100ms. SNR uses all annotated speech samples (not a VAD); a
single common gain keeps the clean reference and mix below peak 0.9 without
clipping. Each scene records source PCM hashes, mixed PCM hash, provenance,
requested SNR, common gain, versions and model identity in `metadata.json`.
Do not commit private recordings, transcripts or generated output.

Each model executable evaluates bypass, Speex, WebRTC Moderate, upstream RNNoise
and nnnoiseless, with AEC off. There are two explicitly separate passes: ANS-only
(`ans` in filenames), and the same choices followed by fixed AGC2 (`agc2`). The
latter approximates one listener's processing but does not include Opus, network,
jitter buffering, device capture or per-member product scheduling. Other engines
in the little build are duplicate controls; only RNNoise changes model.

Outputs:

- `results.csv`, `report.md`: aligned SI-SDR/improvement, speech projection gain,
  pause residual/reduction, per-frame compute and normalized CPU. SI-SDR is a
  waveform proxy, **not MOS/intelligibility or a perceptual ranking**. It may
  penalize harmless spectral changes or miss unpleasant artifacts. A muted
  output has unavailable speech metrics, never a perfect score.
- `answer-key.json`: full per-repeat measurements, metrics, hidden case mappings
  and exact listening gains. `clean_only_quality` and CSV `clean_only_si_sdr_db`
  measure each processor on clean input to expose speech damage. The sample-wise
  correlation alignment searches 0..30ms only for reference comparison, not to
  claim calibrated algorithmic latency; a result at the bound needs investigation.
- `raw/`: unmodified mixed-input outputs, clean reference and each clean-only
  processed output. Use these to judge actual AGC loudness and clean-speech damage.
- `listening/`: anonymous, shuffled candidates, projected speech gain matched
  within +/-12dB, followed by shared peak-safe headroom for all candidates and the
  reference. Severely suppressed outputs can remain quieter due to this bound;
  consult raw outputs and gains. Give listeners only this folder and `scores.csv`,
  keeping reports/answer key separate. Match within each scene and pass; filenames
  intentionally expose the pass but conceal the ANS implementation. Model runs
  have independent folders and are not a combined regular/little blind trial.
  `SOURCES.md` retains manifest provenance and any sibling input `ATTRIBUTION.md`;
  keep it with shared audio. For custom recordings, supply complete attribution
  and any required permissions in those fields/files.
- `scores.csv`: blank human scores for speech clarity, quiet background and lack
  of artifacts (higher is better), plus notes. These are informal listening scores,
  not a standardized MOS study. Complete before revealing the answer key.

Silence measurements exclude 50ms around speech edges. They describe raw output
levels, so AGC can affect them; use ANS-only rows for primary suppression screening.
Preserve all three listening dimensions rather than selecting on suppression
alone. CPU/p99 exclude metrics, WAV I/O, normalization and construction; they are
throughput statistics for one 10ms stream, not added buffering delay, process RSS,
background desktop/GPU occupancy, or gaming frame-time impact. Current settings
are not equal-strength NS presets. AEC quality needs a separate calibrated echo
path/reference test. Product defaults remain unchanged until representative
human listening and Windows equivalent-workload measurements justify a change.
If far is omitted, its reference stays synthetic, as recorded in metadata.

### Recorded-noise Mandarin screening

The second local round uses four THCHS-30 Mandarin readings and recorded
MS-SNSD keyboard, HVAC and competing conversation. It also tests input level
steps and an attenuated-input case; these are amplitude simulations, not natural
whispering or shouting. Sources are 16kHz recordings explicitly resampled to
48kHz. Reference speech may retain its original room noise. The preparation
script pins and verifies source hashes and writes full attribution alongside
the inputs; it downloads about 12MB, not the full THCHS-30 archive.

```sh
python3 test/audio3a/quality-round2.py self-test
python3 test/audio3a/quality-round2.py prepare build/audio3a/quality-round2-input
# Run sequentially, without competing builds or other benchmark processes.
build/audio3a/package/resona-3a-bench quality \
  build/audio3a/quality-round2-input/manifest.json build/audio3a/reports/quality-round2-regular
build/audio3a/package/resona-3a-bench-little quality \
  build/audio3a/quality-round2-input/manifest.json build/audio3a/reports/quality-round2-little
python3 test/audio3a/quality-round2.py summarize \
  build/audio3a/reports/quality-round2-regular build/audio3a/reports/quality-round2-little \
  build/audio3a/reports/quality-round2-summary
python3 test/audio3a/quality-round2.py pack \
  build/audio3a/reports/quality-round2-regular build/audio3a/reports/quality-round2-little \
  build/audio3a/quality-round2-blind
```

Python 3, curl and FFmpeg are required for preparation/analysis/packaging, not for
running prepared inputs with the benchmark executables. Use new output paths
for a rerun. This larger ANS-only round is manual; the codec-chain experiment
below uses these same prepared inputs in the manual Actions workflow.
The summary reports each utterance's projection gain to expose severe
suppression of a quiet segment hidden by a louder one. It does not detect lost
words or replace listening. Background conversation is a difficult negative
control: ordinary noise suppression is not target-speaker separation.

Share only `quality-round2-blind/listening/` for listening. It combines both
RNNoise models and four controls in one jointly gain-matched ANS-only trial,
with no AGC. A-F are shuffled separately in each scenario. Preserve `SOURCES.md`;
the answer key stays outside the listening directory. Output headroom and
gain-matching limits are the same as above, and original within-file dynamics
are preserved. Keep generated inputs, results and listening data out of Git.

### Product codec-chain verification

The manual Actions workflow also packages `resona-chain-regular` and
`resona-chain-little`, plus a hash-verified `chain-input/` directory. These run
sender ANS -> the product's **pure-Go gopus v0.1.1** encoder/decoder -> an
independent receiver AGC2. Each scene gets fresh exclusive sender and receiver
state; no processing state is shared between peers/cases. Off/Speex/RNNoise
are compared at 32/48 kbit/s, 48 kHz mono, 20 ms, VoIP complexity 5. Speex uses
20 ms and -20 dB; AGC2 uses 5 dB headroom, 18 dB maximum adaptive gain and the
core's activity gate. ANS/codec/AGC2 raw WAVs preserve actual gain separately.
This verifies algorithms/configuration through a benchmark FFI adapter, not
desktop devices, AEC, networking, jitter/PLC, mixing, manual gain or the output
limiter. It does not add RNNoise to desktop settings or change defaults.

On Windows, extract the whole artifact and open PowerShell in `package/`.
Close games and competing builds for the first baseline, then run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\run-chain.ps1
```

No microphone permission, server, compiler, FFmpeg or network is required.
The script runs both models sequentially and creates two timestamped result
directories. Keep `metadata.json`, `results.json`, `results.csv`, `report.md`
and `SOURCES.md` together. The bundled CI reports are hosted-runner results;
rerunning the tools collects measurements from your actual PC. This is an
unpaced throughput test, so do not interpret its process CPU while running
as idle-client or in-game CPU. `cpu_percent_one_core` divides process CPU time
by represented audio duration. Go allocation counts exclude native allocations.

Local preparation/analysis (Python and FFmpeg only needed here):

```sh
build/audio3a/package/resona-3a-bench chain-input \
  build/audio3a/quality-round2-input/manifest.json build/audio3a/chain-input
build/audio3a/package/resona-chain-regular \
  -input build/audio3a/chain-input/manifest.json -out build/audio3a/reports/chain-regular
build/audio3a/package/resona-chain-little \
  -input build/audio3a/chain-input/manifest.json -out build/audio3a/reports/chain-little
python3 test/audio3a/quality-round2.py chain-summary \
  build/audio3a/reports/chain-regular build/audio3a/reports/chain-little \
  build/audio3a/reports/chain-summary
```

Use new output directories. Chain inputs require 20 ms boundaries and speech
regions longer than 200 ms. The summary creates a named stage comparison with
**one shared headroom per scene**, across both bitrates and models, without
individual loudness matching. Compare little 04 -> 05 -> 06 and regular
07 -> 08 -> 09: ANS, Opus roundtrip, AGC2. Check for returning background noise,
speech-edge damage and quiet/loud transitions. Pause RMS excludes 100 ms around
speech boundaries without waveform alignment; region RMS still contains residual
noise and is not isolated speech loudness. Neither metric replaces listening.

```sh
ffmpeg -i input.wav -ar 48000 -ac 1 -c:a pcm_f32le speech.wav
./resona-3a-bench --near speech.wav --seconds 15 --warmup 3 --repeats 5 --wavs --out speech-results
```

WAV export is opt-in locally and contains the input and processed audio. Keep
private recordings out of Git and shared CI artifacts. Float WAVs preserve output
above full scale for diagnosis; check peaks and listen at a low device volume.

## Interpret the report

- `mean_us`/p50/p99/p999/max: time per 10 ms capture tick, including render analysis
  when AEC is enabled, frame copying and adapter calls. Model/state creation,
  warmup, quality analysis, JSON/CSV/WAV I/O are excluded. Runs are sequential,
  unpaced throughput loops, not real-time scheduling or gaming frame-time tests.
- `cpu_percent_one_core`: process CPU time / represented audio duration, one core
  = 100%. It includes all native libraries; it is not sampled whole-client CPU.
- `attenuation_db`: input RMS minus output RMS. Useful for echo-only/noise-only;
  **muting all speech also looks good by this metric**. It is not calibrated ERLE,
  perceptual quality, or a speech-preservation score. Do not compare AGC-enabled
  rows as if they were suppression-only measurements.
- `level_thirds_dbfs`: AGC trajectory during quiet/normal/loud input sections;
  also useful when the echo path changes. Not a universal loudness target.
- Peak and clipped sample counts expose saturation. Nonfinite or absolute output
  above 8 fails the run. Silence and legitimate strong suppression are not failures.
- Small repeats do not establish p999 reliability (600 ticks makes p999 effectively
  the maximum). Algorithmic buffering/lookahead, total native memory, allocations,
  real device drift and OS scheduling latency are not measured here. Reported
  processing microseconds must not be presented as algorithmic latency.

Listen to capture/clean/processed WAVs from real Chinese/English voices before
choosing defaults. Add measured room impulse responses, nonlinear speakers, clock
drift and device-switch tests before AEC qualification. Single synthetic harmonic
sources can be treated as noise by speech models and cannot rank them fairly.

## Build locally

Run from the repository root. Install Rust 1.96.0 with llvm-tools-preview, C/C++
compiler, libclang, pkg-config, Python 3, Meson and Ninja. Windows uses MSYS2 MINGW64
and the GNU Rust toolchain (see workflow). macOS can use Homebrew build dependencies.

```sh
bash test/audio3a/build.sh
bash test/audio3a/run-ci.sh
```

`BENCH_RUST_TOOLCHAIN=stable` can use an already installed local toolchain; its exact
version is recorded. `RNNOISE_SOURCE` can point to an existing checkout of the pinned
revision containing the verified `models.tar.gz`. Build output is under ignored
`build/audio3a/`. First build downloads roughly 56 MiB of model data plus APM build
dependencies. Nothing is downloaded during benchmark execution.
