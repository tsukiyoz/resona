# Initial verification (2026-09-16)

Local host: Apple M2 / macOS arm64, Rust 1.96.0. Ran the real bundled WebRTC APM
and current RNNoise source/model, not mocked adapters. RNNoise archive SHA-256
matched the upstream pinned hash before extraction.

- Release build and four focused tests passed: all 13 presets execute finite
  frames; bypass preserves samples and rejects bad choices; fixture generation
  is deterministic; timing quantiles and RMS/attenuation have known scales.
- Full `run-ci.sh`: 13 regular presets x 8 scenes x 3 repeats = 312 rows;
  RNNoise little: 8 scenes x 3 repeats = 24 rows. Both returned zero with empty
  failure lists and produced CSV/JSON/Markdown plus listening WAVs.
- Both executables and dependency notices packaged successfully (about 25 MiB
  on this host, excluding reports/audio).
- x86_64 SIMD dispatcher/kernel syntax checked with Clang, including the upstream
  CPU_INFO_BY_ASM definition. This is not a native Windows/Linux runtime test.
- Shell syntax, Rust formatting, Git whitespace and actionlint checks passed.

Generated evidence is local and ignored under `build/audio3a/reports/`. This is
execution/measurement-pipeline evidence only: synthetic harmonics were heavily
attenuated by the current neural model, illustrating why they cannot establish
speech quality. No module default, AEC quality ranking or whole-client performance
claim is approved by these checks. Windows/Linux execution is delegated to the
new manual GitHub Actions run; inspect its actual result before using its data.

The adjacent Opus packaging fix uses USERPROFILE for Cargo's default registry on
Windows when CARGO_HOME is unset. MSYS HOME had pointed elsewhere; adapter tests
and executable compilation had already passed, but license collection failed.
The script now also reports the missing registry path instead of silently exiting.

## Windows build fixes

- Run 35086271354 reached WebRTC compilation but MinGW `ar` failed to read an
  Abseil object whose absolute path was 260 characters. CI now uses a short
  runner-temporary Cargo target directory (the same object path is 234 characters).
  Packaging still goes under `build/audio3a/`; dependency notices follow the
  selected `CARGO_TARGET_DIR`.
- Run 35087510667 passed archive creation and exposed bundled WebRTC's DLL
  export directives in its static archive. The wrapper prefixes native symbols,
  leaving those export names unresolved. The Windows forced-include header
  disables `WEBRTC_ENABLE_SYMBOL_EXPORT` for static compilation. `winmm` is
  linked after dependency archives so MinGW resolves `timeGetTime`.
- These changes affect benchmark build plumbing, not audio processing settings
  or production dependencies. The four local release adapter tests passed again.
- Run 35088401341 passed all four Windows tests and built both executables, then
  exposed Python's Windows locale decoding of Cargo metadata. License collection
  now explicitly reads Cargo's JSON as UTF-8.
- Run 35089239205 reached the clean-PATH startup check but exited 127. Rust's
  dynamic `stdc++` selection survived the broad `-static` linker argument.
  Windows now requests `static=stdc++` explicitly and supplies GCC's archive
  search directory. The package records its DLL imports in `windows-dlls.txt`.
- [Run 35091667754](https://github.com/tsukiyoz/resona/actions/runs/35091667754)
  passed on both Windows 2022 x64 and Ubuntu 24.04 x64 at commit `059742f`:
  four adapter tests, both executable builds, license packaging, all regular and
  little benchmark cases, and artifact upload. Both Windows executables started
  with only System32 on PATH; the regular executable's import table contained
  only Windows system DLLs, without MinGW runtime DLLs.

## Real-speech screening (2026-10-20)

Local macOS arm64, Rust 1.96.0, release profile. Kept existing source/model pins;
the RNNoise checkout and model archive hash were checked before building. Reused
the cached matching source in `build/opuscodec/rnnoise-upstream`. Build tooling
was local Meson 1.9.1 / Ninja 1.13.0 in an ignored build-directory venv.

- Full `build.sh` passed: nine release tests, regular/little executables and
  dependency-license packaging. The little feature also passed the same nine
  tests independently. The new command test executes all ten combinations,
  checks normalized listening WAVs/CSV shape, rejects output overwrite and
  rejects zero-energy noise without creating an output directory.
- Downloaded the SHA-256-verified VOiCES human-speech sample described in
  `QUALITY-SOURCES.md`. Generated exactly 480000 clean/noise samples (10s),
  including 1s warmup. The clean reference is zero in the annotated pause regions.
- Ran final regular and little tools sequentially, after builds/check processes
  completed: two SNR scenes x ten combinations x three measured repeats = 60
  timing rows per model, 120 total. Each case also has a separate clean-only
  control. Both commands completed successfully. Inputs/hashes match across
  model runs; each score sheet has twenty six-column candidate rows.
- Final local evidence: `build/audio3a/reports/quality-regular/` and
  `build/audio3a/reports/quality-little/`. Private/temporary inputs, audio and
  output are ignored. No microphone, network server or product settings changed.
- Shell syntax, Rust formatting, Git whitespace, YAML parsing and Actionlint
  v1.7.7 passed. Actions now prepares one shared input artifact and evaluates it
  on Windows/Linux. These new remote jobs have **not** been run yet.

Preliminary ANS-only observations, medians across three repeats; CPU is the cost
of one real-time mono stream normalized to a single core, not whole-machine CPU:

| Candidate | CPU (5/15dB scenes) | Silence reduction (5/15dB) |
| --- | --- | --- |
| SpeexDSP | 0.14 / 0.15% | 9.75 / 10.17dB |
| WebRTC Moderate | 0.09 / 0.09% | 14.49 / 14.66dB |
| nnnoiseless | 0.42 / 0.40% | 42.35 / 35.06dB |
| RNNoise regular | 6.16 / 6.26% | 71.42 / 86.00dB |
| RNNoise little | 2.44 / 2.45% | 84.53 / 86.00dB |

The extreme silence-reduction figures are bounded by the -120dBFS computational
floor and do not establish perceptual quality. Neural models substantially
reduce pauses in this fixture, but that does not show whether speech sounds
natural. At 15dB SNR, RNNoise and nnnoiseless lower SI-SDR despite lowering noise;
the tool exposes a tradeoff rather than choosing on noise energy alone. WebRTC
has a lower waveform score here too; phase/filter effects may contribute and
cannot establish an integration defect. These short, bandlimited English samples
with synthetic pink noise cannot select a default, prove Chinese/gaming quality,
compare equal-strength presets, establish AGC convergence, or measure memory,
algorithmic buffering latency or frame-time impact. Human blind scores remain
blank. Representative consented recordings and Windows gaming validation follow.

## Recorded-noise Mandarin screening (2026-10-20)

Same local macOS arm64 host and release executables/model pins as above. New
stdlib Python tooling prepares verified public inputs, summarizes per-utterance
projection gains, and creates a combined regular/little ANS-only blind trial.

- Four THCHS-30 speakers, recorded MS-SNSD keyboard/HVAC/babble, plus amplitude
  level steps and attenuated input: five scenes, 2s warmup and 18s measurement.
  Source URLs, pinned revision, hashes and transformations accompany the inputs
  and listening export. Speech/noise are 16k resampled to 48k, not full-band.
- Rejected the initial C21_517 reference after its strong infrasonic energy
  caused misleading waveform projection and gain matching for high-pass
  processors. A 40Hz high-pass changed source RMS by 4.10dB, compared with 0.11dB
  for replacement C31_736. Selected references were not high-pass filtered.
  Repeated the entire experiment; earlier unsuffixed round2 reports are excluded
  from conclusions. Authoritative local evidence has the `round2-v2` prefix.
- Ran regular and little sequentially, without concurrent builds: 5 scenes x
  10 combinations x 3 repeats x 2 models = 300 timing rows. Each case also has a
  clean-only control. Shared manifests and input hashes match across runs.
- Authoritative reports: `build/audio3a/reports/quality-round2-v2-{regular,little}/`
  and `quality-round2-v2-summary/`. Combined listening export:
  `build/audio3a/quality-round2-v2-blind/listening/`. Six candidates per scene,
  ANS only, newly shuffled A-F per scene, joint gain matching and common headroom.
  Answer key remains outside that directory. Human scores are still blank.
- Preparation/matching self-tests passed. Checked 35 finite 18s WAVs with shared
  peak <= 0.95, thirty six-column score rows, attribution preservation, 300 timing
  rows, and alignment results below the 30ms search bound. ANS-only outputs had
  no full-scale samples. Some separate AGC2 combinations hit exactly full scale
  (the counter uses abs(sample) >= 1, peak 1.0), without values above it. Those
  raw results are retained, not used in the ANS-only blind pack.

ANS-only cost: median of each scene's three-repeat median, normalized to one
real-time core. Suppression refers to pauses, not speech quality:

| Candidate | CPU % | Keyboard pause reduction dB | HVAC pause reduction dB |
| --- | ---: | ---: | ---: |
| SpeexDSP -20dB | 0.145 | 0.20 | 17.47 |
| WebRTC Moderate | 0.082 | 0.80 | 14.45 |
| nnnoiseless 0.5.2 | 0.402 | 2.60 | 43.00 |
| RNNoise regular | 5.990 | 32.55 | 78.70 |
| RNNoise little | 2.453 | 36.90 | 86.75 |

Little costs about 59% less CPU than regular here. In keyboard/HVAC scenes its
SI-SDR improvements are +5.45/+5.21dB versus regular +5.76/+5.04dB. The level-step
quiet utterance projects at -2.17dB for little versus -1.98dB for regular; the
soft HVAC case remains at about -0.7dB per utterance, without total suppression.
These are waveform checks, not intelligibility or naturalness verdicts.

Competing conversation is a limitation: little improves SI-SDR by -0.32dB,
regular +0.31dB, nnnoiseless +2.35dB. Little's first utterance projection falls
4.11dB, so stronger pause suppression does not establish a better overall result.
Listen for desired-speech damage and background voices. All preset strengths
remain unequal. RNNoise little is the leading integration candidate, pending
this blind listening and Windows equivalent-workload qualification; no product
default/dependency changed. This test does not measure game low-frame impact,
end-to-end latency, RSS, AEC, Opus, network or device behavior.

### Informal round 2 listening feedback

One maintainer completed the five scenes before revealing the per-scene answer
key. No numeric scores or statistical significance are inferred:

- Keyboard: C (little) and D (regular) suppressed it; other candidates did not.
- HVAC: A (Speex), D (regular), F (little) acceptable; B (nnnoiseless) had odd
  noise leakage during speech; bypass/WebRTC Moderate insufficient.
- Background speech: D (regular) marginally acceptable with slight voice change
  and occasional leakage; E (little) also marginally acceptable but stranger
  voice than D. nnnoiseless sounded disordered; other choices retained noise.
- Level steps: D (regular) removed keyboard and E (little) mostly did; other
  choices retained it. Per-utterance speech completeness was not explicitly rated.
- Soft input: only B (bypass) had audible HVAC; all five denoisers were quiet.
  Desired-speech completeness was not separately rated, so no winner is claimed.

The listening supports prioritizing little for cost and regular as an optional
quality candidate, rather than choosing solely on pause suppression or SI-SDR.
Real product-chain, gaming-capture and Windows workload validation remain open;
the controlled regular/little Mandarin listening step is now complete. Full
qualitative notes are local/ignored in `build/audio3a/reports/listening-feedback.md`.

## ANS -> product codec -> per-peer AGC2 (2026-10-20)

Added an isolated Go runner using the product's pure-Go `gopus v0.1.1`, with
48kHz mono/20ms, VoIP, complexity 5 and 32/48kbit/s targets. Each case owns fresh
sender ANS and receiver AGC2 states. The Rust static adapter uses the same pinned
RNNoise models; Speex now has a separate 20ms/-20dB control matching the core.
AGC2 uses headroom 5dB, maximum 18dB, initial 0dB, and the core's activity gate.
No desktop dependency/default changed. No AEC/devices/network/jitter/PLC/mixing,
manual gain or final output limiter is exercised.

Local Apple M2/macOS arm64 release build (macOS deployment target 26.0), Go
1.26.2 and Rust stable 1.96.0. Rebuilt WebRTC with bundled Abseil in an independent
target directory; tools link only system macOS dylibs, not Homebrew dylibs.
Ten Rust tests and three Go tests passed; Go tests passed for both model builds,
including independent deterministic histories, actual Opus roundtrip, input hash
and bounds rejection, activity gate and level metrics. `make lint`, Actionlint
1.7.7, round2 Python self-tests, shell syntax, Rust/Go formatting and diff checks
passed. Existing Keychain API deprecation and duplicate system-library link
warnings remain; these are not benchmark failures.

The same verified mixed float PCM from round2-v2 produced 5 scenes x 3 ANS
choices x 2 bitrates x 3 repeats x 2 model builds = 180 timing rows. Runs were
sequential with no competing builds. Median across rows for each choice:

| Full chain ANS choice | One-core real-time CPU % | Median per-run p99 us |
| --- | ---: | ---: |
| Off | 1.17 | 299 (regular-build controls) |
| Speex -20dB / 20ms | 1.33 | 333 |
| RNNoise little | 3.48 | 865 |
| RNNoise regular | 7.20 | 1774 |

These include benchmark validation, timing and stage copies; exclude construction,
analysis and disk I/O. They are throughput CPU cost, not idle app CPU or game-frame
impact. Roughly one Go allocation per packet is observed; native allocations are
not counted. Original timings include outliers: little keyboard/32k repeat 2 had
p99 13.51ms and max 50.92ms; regular's worst max was 28.98ms. A targeted five-repeat
little keyboard/32k recheck had CPU median 3.50%, p99 median 0.873ms, worst max
2.818ms. The original outliers remain in the reports. Their cause is unproven;
this unpaced experiment cannot establish real-time scheduling guarantees.

Keyboard/32k pause levels after decode -> AGC2: Speex -39.3 -> -29.8dBFS,
little -76.3 -> -68.1dBFS, regular -71.5 -> -61.1dBFS. AGC2 raises residual
noise where active, but the neural choices retain substantially lower pause
levels here. HVAC neural residuals remain below -100dBFS after the activity gate;
do not treat the computational floor as a perceptual score. Background speech
still needs listening. In the level-step scene AGC2 does not immediately equalize
the first quiet utterance: little's three decoded region RMS values are about
-43.7/-30.9/-24.6dBFS, becoming -36.0/-17.0/-16.6dBFS. This is adaptation, not
proof of quiet-speech completeness; region RMS also contains residual noise.

All ANS/decoded outputs were below full scale. Across 180 rows, AGC2 reached
exactly 1.0 on 18 samples, none exceeded 1.0. These repeat counts are diagnostics,
not 18 distinct audible clipping events. Raw float WAVs retain gain. The named
stage listening pack uses common scene headroom across both models/rates,
without individual loudness matching. It contains 100 finite 18s WAVs and full
attribution; summary has 40 rows. Local outputs (ignored):
`build/audio3a/reports/chain-{regular,little,summary}/` and
`chain-little-keyboard-recheck/`. Human chain listening remains pending.

Manual Windows/Linux Actions now build the Go tools, verify independent state,
run the identical chain fixture and upload tools, raw stages and reports.
`package/run-chain.ps1` reruns both models sequentially on the user's PC, without
a compiler, network or audio permission. New platform results and real-device/
gaming validation remain pending until those runs actually succeed.
