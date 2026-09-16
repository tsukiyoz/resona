#!/usr/bin/env bash
set -euo pipefail
root=$(pwd)
if command -v cygpath >/dev/null; then root=$(cygpath -m "$root"); fi
export CARGO_TARGET_DIR="$root/build/audio3a/target"
export RNNOISE_SOURCE=${RNNOISE_SOURCE:-$root/build/audio3a/rnnoise-source}
revision=70f1d256acd4b34a572f999a05c87bf00b67730d
model_hash=0a8755f8e2d834eff6a54714ecc7d75f9932e845df35f8b59bc52a7cfe6e8b37
if [[ ! -d "$RNNOISE_SOURCE/.git" ]]; then
    git clone https://github.com/xiph/rnnoise.git "$RNNOISE_SOURCE"
    git -C "$RNNOISE_SOURCE" checkout --detach "$revision"
fi
test "$(git -C "$RNNOISE_SOURCE" rev-parse HEAD)" = "$revision"
archive="$RNNOISE_SOURCE/models.tar.gz"
if [[ ! -f "$archive" ]]; then
    curl -fLsS --retry 3 --max-time 900 \
        "https://media.xiph.org/rnnoise/models/rnnoise_data-$model_hash.tar.gz" -o "$archive.part"
    mv "$archive.part" "$archive"
fi
if command -v sha256sum >/dev/null; then
    actual=$(sha256sum "$archive" | cut -d ' ' -f 1)
else
    actual=$(shasum -a 256 "$archive" | cut -d ' ' -f 1)
fi
if [[ "$actual" != "$model_hash" ]]; then
    echo "RNNoise model checksum mismatch: $archive" >&2
    exit 1
fi
# GNU tar treats a Windows drive colon in -f as a remote host; stdin is portable.
tar -xzf - -C "$RNNOISE_SOURCE" < "$archive"
