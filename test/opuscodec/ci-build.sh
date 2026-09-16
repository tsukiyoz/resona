#!/usr/bin/env bash
set -euo pipefail
root=$(pwd)
if command -v cygpath >/dev/null; then root=$(cygpath -m "$root"); fi
build="$root/build/opuscodec"
: "${BENCH_TARGET:?Set BENCH_TARGET to a Rust target triple}"
: "${BENCH_PLATFORM:?Set BENCH_PLATFORM to the artifact platform name}"
toolchain=${BENCH_RUST_TOOLCHAIN:-1.96.0}
mkdir -p "$build/package" "$build/reports"
expected=22244de5a79bd1d6d623c32e72bf1954b56235be
test "$(git -C "$build/opus-source" rev-parse HEAD)" = "$expected"
cmake -S "$build/opus-source" -B "$build/opus-build" -G "${BENCH_CMAKE_GENERATOR:-Ninja}" \
    -DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX="$build/opus-install" -DCMAKE_INSTALL_LIBDIR=lib \
    -DBUILD_SHARED_LIBS=OFF -DOPUS_BUILD_TESTING=OFF -DOPUS_BUILD_PROGRAMS=OFF
cmake --build "$build/opus-build" --parallel 2
cmake --install "$build/opus-build"
export PKG_CONFIG_PATH="$build/opus-install/lib/pkgconfig"
export CARGO_TARGET_DIR="$build/rust-target"
cargo +"$toolchain" rustc --locked --release --target "$BENCH_TARGET" \
    --manifest-path test/opuscodec/rust/Cargo.toml -- --print native-static-libs 2>&1 | tee "$build/reports/rust-build.txt"
native_libs=$(sed -n 's/^note: native-static-libs: //p' "$build/reports/rust-build.txt" | tail -1)
test -n "$native_libs"
export CGO_ENABLED=1
export CC=gcc
export CGO_LDFLAGS="-L$build/rust-target/$BENCH_TARGET/release -lresona_opus_bench $native_libs"
suffix=
if [[ "$BENCH_PLATFORM" == windows-* ]]; then
    suffix=.exe
    export CGO_LDFLAGS="$CGO_LDFLAGS -static"
fi
{
    git rev-parse HEAD
    printf 'libopus commit: %s\nplatform: %s\n' "$expected" "$BENCH_PLATFORM"
    uname -a
    go version
    rustc +"$toolchain" -Vv
    gcc --version
    cmake --version
    pkg-config --modversion opus
    printf 'CGO_LDFLAGS=%s\n' "$CGO_LDFLAGS"
    if command -v lscpu >/dev/null; then lscpu; fi
    if [[ -n "$suffix" ]]; then
        powershell.exe -NoProfile -Command 'Get-CimInstance Win32_Processor | Select-Object Name,NumberOfCores,NumberOfLogicalProcessors | Format-List'
    fi
} > "$build/reports/build-info.txt"
cd test/opuscodec
go test -count=1 ./...
go build -trimpath -o "$build/package/opus-bench$suffix" .
cp README.md EVALUATION.md "$build/package/"
cp go.mod go.sum rust/Cargo.lock "$build/reports/"
mkdir -p "$build/package/licenses"
cp "$build/opus-source/COPYING" "$build/package/licenses/libopus-COPYING"
cp "$build/opus-source/LICENSE_PLEASE_READ.txt" "$build/package/licenses/libopus-LICENSE_PLEASE_READ.txt"
gopus_dir=$(go list -m -f '{{.Dir}}' github.com/thesyncim/gopus)
if command -v cygpath >/dev/null; then gopus_dir=$(cygpath -m "$gopus_dir"); fi
cp "$gopus_dir/LICENSE" "$build/package/licenses/gopus-LICENSE"
cargo_home=${CARGO_HOME:-$HOME/.cargo}
if [[ -n "$suffix" && -z "${CARGO_HOME:-}" ]]; then cargo_home="$USERPROFILE/.cargo"; fi
if command -v cygpath >/dev/null; then cargo_home=$(cygpath -m "$cargo_home"); fi
for crate in opus-rs-0.1.33 rusty-opus-0.9.1; do
    files=("$cargo_home"/registry/src/*/"$crate"/COPYING)
    if [[ ! -f "${files[0]}" ]]; then
        echo "Missing Cargo registry license for $crate under $cargo_home" >&2
        exit 1
    fi
    cp "${files[0]}" "$build/package/licenses/$crate-COPYING"
done
if [[ -n "$suffix" ]]; then
    # Prove that downloaded tools do not need the runner's MinGW DLLs in PATH.
    PATH=/c/Windows/System32 "$build/package/opus-bench.exe" -h
fi
