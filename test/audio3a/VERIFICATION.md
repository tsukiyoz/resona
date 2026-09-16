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
