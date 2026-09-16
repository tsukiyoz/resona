# Opus implementation benchmark

An isolated experiment, not a product dependency or codec migration. The root Go
module and desktop dependencies are unchanged. Supported hosts: macOS/Linux with
cgo, a C compiler, pkg-config, libopus headers/library, Go and Rust.
Windows x64 builds use the separate CI script with MinGW and the Rust GNU target.

## GitHub Actions and downloaded tools

`Opus codec benchmark` is a separate, manually triggered workflow. Once merged
onto the default branch, use Actions -> Opus codec benchmark -> Run workflow.
It does not run on every product push or release. It requires no repository
Secrets and grants only read access to repository contents.

The matrix runs natively on Windows 2022 x64 and Ubuntu 24.04 x64, not in emulated
containers. Both build official libopus 1.6.1 from commit
`22244de5a79bd1d6d623c32e72bf1954b56235be`, plus the locked Rust dependencies with
Rust 1.96.0. Windows uses the GNU Rust target to match the cgo MinGW ABI. The
Windows executable is statically linked and checked with MinGW removed from PATH.
Linux tools still require a compatible host libc (build baseline Ubuntu 24.04).

Each job tests the harness, then sequentially measures CBR and exploratory VBR
with the same deterministic synthetic fixture, 15 measured seconds, five repeats,
20/32/48 kbps and 1/4/8 decoders. No upstream speech recording is downloaded.
CI results do not have the same input as the initial speech evaluation below.
Use shared-runner results for correctness and broad trends; rerun on a quiet
gaming PC before making a performance decision. Comparing OS rows also compares
different runner hardware and is not an isolated OS comparison.

Artifacts named `opus-benchmark-<platform>-<commit>` are kept for 14 days:

- `package/`: executable, README and initial evaluation.
- `reports/`: build provenance, lockfiles, per-mode CSV/JSON/Markdown and exit status.
- The Actions job summary also displays each available mode's report.

A codec investigation gate failure makes the job red **after both modes run**;
reports and the executable are still uploaded. In particular, opus-rs currently
fails the waveform comparison on local fixtures. Build/unit-test failures stop
measurement; partial logs are uploaded when available. A red job must not be
interpreted as a passing codec qualification just because artifacts exist.

After extracting the Windows artifact, open PowerShell in `package/`:

```powershell
.\opus-bench.exe -seconds 15 -repeats 5 -out .\results-cbr
.\opus-bench.exe -seconds 15 -repeats 5 -cbr=false -out .\results-vbr
# Optional normalized 48 kHz mono float32 speech, at least 16 seconds:
.\opus-bench.exe -pcm C:\audio\speech-48k.f32 -seconds 15 -repeats 5 -out .\results-speech
```

No Go/Rust toolchain, microphone permission or server connection is needed to
run the downloaded executable. Results are written only to the selected output
directory. On Linux use `chmod +x opus-bench` then `./opus-bench` with the same flags.

## Implementations

| Name | Version / source commit | Measured call path |
| --- | --- | --- |
| gopus | `github.com/thesyncim/gopus v0.1.1`, checksummed in go.sum | Go directly |
| libopus | System `pkg-config opus`; exact runtime version in metadata | Go -> cgo -> official C library |
| opus-rs | `0.1.33`, `be9884654b1fe018baee6859cf4e00b7a11ec9d2` | Go -> cgo -> Rust static library |
| rusty-opus | `0.9.1`, `c4af6337ef329f4cb2cd6bd48e84fd121d721780` | Go -> cgo -> Rust static library |

Rust releases and checksums are pinned in Cargo.lock. Source commits come from
the published `.cargo_vcs_info.json`. These are both pure Rust codecs, not two
wrappers around libopus. No fork/patch of a candidate is used. Library README
performance and production-readiness claims are not used as evidence.

The experiment asks whether **replacing the codec inside today's Go core** helps.
It does not benchmark a Rust core or predict mobile battery life. C and Rust each
cross cgo once per frame; the empty bridge row measures this overhead separately.
Rust adapters catch panics rather than unwinding across C. Single-owner handles
are destroyed after each run and never retain pointers to Go PCM/packet buffers.

## Run

On macOS prerequisites include `brew install opus pkg-config` and installed Go/Rust.
From repository root:

```sh
bash test/opuscodec/run.sh
```

The script builds release Rust code and a Go runner, then runs sequentially.
Raw files are ignored under `build/opuscodec/<UTC timestamp>/`:

- `report.md`: repeated-run medians, interoperability and encoder output checks.
- `results.csv` / `results.json`: every repetition, tail timings and errors.
- `interop.json`: each encoder decoded by all four decoders.
- `quality.json`: actual bitrate, codec mode counts, aligned waveform SNR.
- `metadata.json`, `build-info.txt`, `Cargo.lock`: inputs, versions and build flags.

Default input is a deterministic harmonic/noise/quiet fixture. For actual speech,
convert a local file **once**, then give exactly the same PCM to every codec:

```sh
ffmpeg -i speech.wav -ar 48000 -ac 1 -f f32le /tmp/speech-48k.f32
bash test/opuscodec/run.sh -pcm /tmp/speech-48k.f32 -seconds 15 -repeats 5
bash test/opuscodec/run.sh -pcm /tmp/speech-48k.f32 -seconds 15 -repeats 5 -cbr=false
```

Input must be normalized float32 little-endian mono, 48 kHz, and at least
`seconds + 1` seconds long. The first second warms codec state; measurements use
the subsequent contiguous frames. No implicit looping or resampling occurs in
the timed section. SHA-256 covers the exact PCM prefix used.

CBR is the default common configuration: 48 kHz mono, 20 ms, VoIP, complexity 5,
20/32/48 kbps, automatic mode/bandwidth, FEC/DTX off and expected loss 0. Each CBR
packet is checked against the requested byte count. Complexity can be overridden
with `-complexity 0..10`. VBR is exploratory: C/Go enable constrained VBR, while
the Rust implementations make their own internal mode-dependent decisions. It is
not valid to assume identical quality, bandwidth or rate allocation from the
same public control values. The product's existing VBR defaults are not changed.

## Measurement rules

- Every repeat constructs fresh state, warms it, then measures codec calls. File
  I/O, construction, report generation and validation are excluded. Codecs rotate
  order between repeats; run only one benchmark at a time on a quiet machine.
- Decode uses **the same libopus-encoded packets** for every implementation.
  1/4/8 streams use independent decoder state, processed sequentially each tick.
  This isolates decode cost; all streams use the same corpus. It does not model
  different speakers, mixing, device callbacks, packet loss, or a live call.
- Mean wall time and process CPU time are per 20 ms tick; decode N includes all N
  decoders. CPU percentage is `CPU microseconds per tick / 200`, normalized to
  one core. It is a capacity estimate, not a sampled OS CPU percentage.
- Per-tick p50/p99/p999/max use a monotonic clock and nearest-rank quantiles.
  Timing overhead remains included. These are throughput-loop observations,
  not paced real-time deadlines or end-to-end voice/network latency. With fewer
  than 1000 ticks, p999 is effectively a maximum; inspect sample counts.
- Default GC remains enabled. Go allocations omit all C/Rust heap allocations,
  so they must not be used to compare total memory across implementations.
- Cross-decode checks validate frame length, finite/bounded samples, non-silent
  output and waveform NRMSE against libopus **on the same packet stream**.
  NRMSE > 0.05 is an investigation gate, not a standards-conformance threshold.
- Encoder output is decoded with libopus; delay-aligned SNR and mode counts help
  detect large differences. SNR is not a perceptual quality score, and is not
  sufficient to rank speech codecs. No gain fitting is applied.
- A failed codec/interop gate preserves the report and returns nonzero. A fast
  candidate with a failed gate is not eligible for a transparent replacement.
  This is intentional, not a reason to suppress failures or delete rows.

## Harness checks

After building with run.sh:

```sh
cd test/opuscodec
CGO_ENABLED=1 CGO_LDFLAGS="-L$(pwd)/../../build/opuscodec/rust-target/release" go test ./...
```

Tests cover raw PCM validation, deterministic input, quantiles, waveform error,
delay alignment and libopus CBR roundtrip. A short full smoke run exercises all
four adapters: `bash test/opuscodec/run.sh -seconds 1 -repeats 1`.

## Local speech fixture provenance

The initial local evaluation additionally uses the upstream `opus-rs` example
`fixtures/answer_16k.wav` from commit
`be9884654b1fe018baee6859cf4e00b7a11ec9d2`:

<https://github.com/restsend/opus-rs/blob/be9884654b1fe018baee6859cf4e00b7a11ec9d2/fixtures/answer_16k.wav>

Downloaded WAV SHA-256:
`5ad10281c60bcf6de675c1d332e378bb77538bd3be39666ff2d5a3ef03f4ef42`.
Source is 18.040813 seconds, 16 kHz mono, resampled once using ffmpeg to 48 kHz.
Upsampling does not create full-band speech content. The audio is not vendored,
redistributed or included in product artifacts; source audio licensing was not
independently established. Use your own authorized multi-speaker corpus for
release qualification. No user microphone, private audio or live server is used.

Before adopting any codec, separately qualify English/Chinese speakers, noisy
microphones, wideband recordings, packet loss/PLC/FEC, malformed inputs, Windows
and ARM/x86 behavior, and listening quality. A faster benchmark alone does not
justify rewriting the core or replacing the product codec.
