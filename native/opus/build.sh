#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
if command -v cygpath >/dev/null; then root=$(cygpath -m "$root"); fi
source_dir="$root/build/native/opus-source"
revision=22244de5a79bd1d6d623c32e72bf1954b56235be
if [[ ! -d "$source_dir/.git" ]]; then
    git init "$source_dir"
    git -C "$source_dir" remote add origin https://github.com/xiph/opus.git
fi
if ! git -C "$source_dir" rev-parse --verify HEAD >/dev/null 2>&1; then
    git -C "$source_dir" fetch --depth 1 origin "$revision"
    git -C "$source_dir" checkout --detach "$revision"
fi
test "$(git -C "$source_dir" rev-parse HEAD)" = "$revision"
if ! git -C "$source_dir" rev-parse --verify refs/tags/v1.6.1 >/dev/null 2>&1; then
    git -C "$source_dir" fetch --depth 1 origin refs/tags/v1.6.1:refs/tags/v1.6.1
fi
test "$(git -C "$source_dir" rev-parse 'v1.6.1^{commit}')" = "$revision"
cmake -S "$source_dir" -B "$root/build/native/opus-build" \
    -DCMAKE_PROJECT_Opus_INCLUDE="$root/native/opus/apple-neon.cmake" \
    -DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX="$root/build/native/opus-install" \
    -DCMAKE_INSTALL_LIBDIR=lib -DBUILD_SHARED_LIBS=OFF \
    -DOPUS_BUILD_TESTING=OFF -DOPUS_BUILD_PROGRAMS=OFF
cmake --build "$root/build/native/opus-build" --config Release --parallel 2
cmake --install "$root/build/native/opus-build" --config Release
