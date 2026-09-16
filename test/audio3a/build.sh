#!/usr/bin/env bash
set -euo pipefail
source test/audio3a/prepare.sh
build="$root/build/audio3a"
mkdir -p "$build/package" "$build/reports"
toolchain=${BENCH_RUST_TOOLCHAIN:-1.96.0}
suffix=
if [[ "$(uname -s)" == MINGW* ]]; then
    suffix=.exe
    export CC=gcc CXX=g++
    export LIBCLANG_PATH
    LIBCLANG_PATH=$(cygpath -m /mingw64/bin)
    export BINDGEN_EXTRA_CLANG_ARGS=--target=x86_64-w64-windows-gnu
    # Bundled Meson enables DLL exports even for static archives. Include after
    # command-line defines so objcopy's symbol prefixing leaves no stale exports.
    export CFLAGS="${CFLAGS:-} -include $root/test/audio3a/windows-static.h"
    export CXXFLAGS="${CXXFLAGS:-} -include $root/test/audio3a/windows-static.h"
    # cc otherwise requests a dynamic libstdc++, overriding the broad -static.
    export CXXSTDLIB=static=stdc++
    cxx_library=$(g++ -print-file-name=libstdc++.a)
    test -f "$cxx_library"
    export RUSTFLAGS="-C target-feature=+crt-static -C link-arg=-static -L native=$(dirname "$cxx_library")"
fi
{
    git rev-parse HEAD
    rustc +"$toolchain" -Vv
    uname -a
    meson --version
    ninja --version
    printf 'RNNoise commit=%s\nmodel SHA256=%s\nRUSTFLAGS=%s\n' "$revision" "$model_hash" "${RUSTFLAGS:-}"
    if command -v lscpu >/dev/null; then lscpu; fi
    if [[ -n "$suffix" ]]; then
        powershell.exe -NoProfile -Command 'Get-CimInstance Win32_Processor | Select-Object Name,NumberOfCores,NumberOfLogicalProcessors | Format-List'
    fi
} > "$build/reports/build-info.txt"
cargo +"$toolchain" test --release --locked --manifest-path test/audio3a/Cargo.toml
cargo +"$toolchain" build --release --locked --manifest-path test/audio3a/Cargo.toml
cp "$CARGO_TARGET_DIR/release/resona-3a-bench$suffix" "$build/package/"
cargo +"$toolchain" build --release --locked --features rnnoise-little --manifest-path test/audio3a/Cargo.toml
cp "$CARGO_TARGET_DIR/release/resona-3a-bench$suffix" "$build/package/resona-3a-bench-little$suffix"
cp test/audio3a/README.md "$build/package/"
cp test/audio3a/Cargo.lock "$build/reports/"
cargo +"$toolchain" metadata --locked --format-version 1 --manifest-path test/audio3a/Cargo.toml > "$build/reports/dependencies.json"
# Keep license notices for the statically linked dependencies alongside the tools.
python3 test/audio3a/licenses.py "$build/reports/dependencies.json" "$build/package/licenses" "$RNNOISE_SOURCE"
if [[ -n "$suffix" ]]; then
    objdump -p "$build/package/resona-3a-bench.exe" | grep 'DLL Name:' | tee "$build/reports/windows-dlls.txt"
    PATH=/c/Windows/System32 "$build/package/resona-3a-bench.exe" --help
    PATH=/c/Windows/System32 "$build/package/resona-3a-bench-little.exe" --help
fi
