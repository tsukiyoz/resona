#!/usr/bin/env bash
set -euo pipefail
source test/audio3a/prepare.sh
build="$root/build/audio3a"
mkdir -p "$build/package" "$build/reports"
toolchain=${BENCH_RUST_TOOLCHAIN:-1.96.0}
if [[ "$(uname -s)" == Darwin ]]; then
    # Use bundled Abseil rather than leaking Homebrew dylib paths into the tools.
    export PKG_CONFIG_LIBDIR=/nonexistent
fi
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
build_chain() {
    local model=$1
    shift
    cargo +"$toolchain" rustc --lib --release --locked "$@" --manifest-path test/audio3a/Cargo.toml -- --print native-static-libs 2>&1 | tee "$build/reports/chain-$model-link.txt"
    local native_libs
    native_libs=$(sed -n 's/^note: native-static-libs: //p' "$build/reports/chain-$model-link.txt" | tail -1)
    test -n "$native_libs"
    local links="-L$CARGO_TARGET_DIR/release $native_libs"
    # The bundled WebRTC archive leaves absl_strings as a separate link input.
    local absl
    absl=$(find "$CARGO_TARGET_DIR/release/build" -path '*/webrtc-audio-processing-build/*' -name libabsl_strings.a -print -quit)
    test -n "$absl"
    links="$links -L$(dirname "$absl")"
    if [[ -n "$suffix" ]]; then links="$links -static -lwinmm"; fi
    (cd test/audio3a/chain
        CGO_ENABLED=1 CGO_LDFLAGS="$links" go test -count=1 ./...
        CGO_ENABLED=1 CGO_LDFLAGS="$links" go build -trimpath -o "$build/package/resona-chain-$model$suffix" .
    )
}
build_chain regular
cargo +"$toolchain" build --release --locked --features rnnoise-little --manifest-path test/audio3a/Cargo.toml
cp "$CARGO_TARGET_DIR/release/resona-3a-bench$suffix" "$build/package/resona-3a-bench-little$suffix"
build_chain little --features rnnoise-little
cp test/audio3a/README.md "$build/package/"
cp test/audio3a/quality-example.json test/audio3a/QUALITY-SOURCES.md "$build/package/"
cp test/audio3a/run-chain.ps1 "$build/package/"
cp test/audio3a/chain/go.mod "$build/reports/chain-go.mod"
cp test/audio3a/chain/go.sum "$build/reports/chain-go.sum"
gopus_dir=$(cd test/audio3a/chain && go list -m -f '{{.Dir}}' github.com/thesyncim/gopus)
if command -v cygpath >/dev/null; then gopus_dir=$(cygpath -m "$gopus_dir"); fi
cp test/audio3a/Cargo.lock "$build/reports/"
cargo +"$toolchain" metadata --locked --format-version 1 --manifest-path test/audio3a/Cargo.toml > "$build/reports/dependencies.json"
# Keep license notices for the statically linked dependencies alongside the tools.
python3 test/audio3a/licenses.py "$build/reports/dependencies.json" "$build/package/licenses" "$RNNOISE_SOURCE"
cp "$gopus_dir/LICENSE" "$build/package/licenses/gopus-LICENSE"
if [[ -n "$suffix" ]]; then
    objdump -p "$build/package/resona-3a-bench.exe" | grep 'DLL Name:' | tee "$build/reports/windows-dlls.txt"
    PATH=/c/Windows/System32 "$build/package/resona-3a-bench.exe" --help
    PATH=/c/Windows/System32 "$build/package/resona-3a-bench-little.exe" --help
    PATH=/c/Windows/System32 "$build/package/resona-chain-regular.exe" -h
    PATH=/c/Windows/System32 "$build/package/resona-chain-little.exe" -h
fi
