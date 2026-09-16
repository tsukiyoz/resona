#!/usr/bin/env bash
set -euo pipefail
here=$(cd -- "$(dirname -- "$0")" && pwd)
root=$(cd -- "$here/../.." && pwd)
build="$root/build/opuscodec"
mkdir -p "$build"
pkg-config --exists opus
export CARGO_TARGET_DIR="$build/rust-target"
cargo build --locked --release --manifest-path "$here/rust/Cargo.toml"
export CGO_ENABLED=1
export CGO_LDFLAGS="${CGO_LDFLAGS:-} -L$CARGO_TARGET_DIR/release"
cd "$here"
go build -trimpath -o "$build/opus-bench" .
out="$build/$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$out"
{
    git -C "$root" rev-parse HEAD
    git -C "$root" status --short
    uname -a
    go version
    rustc -Vv
    pkg-config --modversion opus
    pkg-config --variable=libdir opus
    printf 'RUSTFLAGS=%s\nCGO_CFLAGS=%s\nCGO_LDFLAGS=%s\n' "${RUSTFLAGS:-}" "${CGO_CFLAGS:-}" "$CGO_LDFLAGS"
    if [[ $(uname -s) == Darwin ]]; then sysctl -n machdep.cpu.brand_string; fi
} > "$out/build-info.txt"
cp "$here/rust/Cargo.lock" "$out/Cargo.lock"
"$build/opus-bench" -out "$out" "$@"
