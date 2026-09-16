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
  10 ms consistently across algorithms, so this is not an exact product profile.
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
CI inputs are deterministic harmonic signals with noise/transients, **not speech**.
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
If far is omitted, its reference stays synthetic, as recorded in metadata.

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
