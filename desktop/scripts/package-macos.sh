#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
desktop_dir=$(dirname -- "$script_dir")
repo_dir=$(dirname -- "$desktop_dir")
bundle_version=$(cat "$repo_dir/VERSION")
if ! printf '%s\n' "$bundle_version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo 'Invalid root VERSION' >&2; exit 1
fi
bundle_dir=${RESONA_BUNDLE_DIR:-"$desktop_dir/dist/Resona.app"}
profile=${RESONA_CARGO_PROFILE:-release}
sign_identity=$(sh "$script_dir/macos-signing-identity.sh")

case "$profile" in
  debug|release) ;;
  *) echo "RESONA_CARGO_PROFILE must be debug or release" >&2; exit 1 ;;
esac

case "$bundle_dir" in
  "$desktop_dir/dist/"*.app) ;;
  *) echo "bundle output must be an .app below $desktop_dir/dist" >&2; exit 1 ;;
esac

make -C "$repo_dir" build-opus build-apm
export PKG_CONFIG_PATH="$repo_dir/build/native/opus-install/lib/pkgconfig"
export OPUS_STATIC=1
export RESONA_VERSION=${RESONA_VERSION:-v$bundle_version}
export RESONA_BUILD_TIME=${RESONA_BUILD_TIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}

if [ "$profile" = release ]; then
  cargo build --locked --manifest-path "$desktop_dir/Cargo.toml" --release
else
  cargo build --locked --manifest-path "$desktop_dir/Cargo.toml"
fi

icon_tmp=$(mktemp -d "${TMPDIR:-/tmp}/resona-icon.XXXXXX")
trap 'rm -rf "$icon_tmp"' EXIT HUP INT TERM
mkdir "$icon_tmp/Resona.iconset"
for size in 16 32 128 256 512; do
  sips -z "$size" "$size" "$desktop_dir/assets/resona-macos-light.png" \
    --out "$icon_tmp/Resona.iconset/icon_${size}x${size}.png" >/dev/null
  double_size=$((size * 2))
  sips -z "$double_size" "$double_size" "$desktop_dir/assets/resona-macos-light.png" \
    --out "$icon_tmp/Resona.iconset/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil -c icns "$icon_tmp/Resona.iconset" -o "$icon_tmp/Resona.icns"

if [ -d "$bundle_dir" ] && [ ! -f "$bundle_dir/Contents/Resources/.resona-bundle" ] && [ ! -f "$bundle_dir/Contents/.resona-bundle" ]; then
  echo "refusing to replace an unrecognized app bundle: $bundle_dir" >&2
  exit 1
fi
rm -rf "$bundle_dir"
mkdir -p "$bundle_dir/Contents/MacOS" "$bundle_dir/Contents/Resources"
touch "$bundle_dir/Contents/Resources/.resona-bundle"
cp "$desktop_dir/target/$profile/resona-desktop" "$bundle_dir/Contents/MacOS/resona-desktop"
apm_binary="$repo_dir/build/bin/libresona_webrtc_apm.dylib"
if [ ! -f "$apm_binary" ]; then
  echo "WebRTC audio library is missing: $apm_binary" >&2
  exit 1
fi
mkdir -p "$bundle_dir/Contents/Frameworks"
cp "$apm_binary" "$bundle_dir/Contents/Frameworks/"
if otool -L "$bundle_dir/Contents/Frameworks/libresona_webrtc_apm.dylib" | tail -n +2 | grep -E '/opt/homebrew|/usr/local|/Users/'; then
  echo 'WebRTC library has non-system dynamic dependencies; rebuild with bundled Abseil' >&2
  exit 1
fi
cp "$icon_tmp/Resona.icns" "$bundle_dir/Contents/Resources/Resona.icns"
cp "$repo_dir/docs/noise-license.txt" "$bundle_dir/Contents/Resources/noise-license.txt"
python3 "$repo_dir/native/webrtc-apm/licenses.py" "$repo_dir/native/webrtc-apm/Cargo.toml" \
  "$bundle_dir/Contents/Resources/licenses" "$repo_dir/native/webrtc-apm/target"
cp "$repo_dir/build/native/opus-source/COPYING" "$bundle_dir/Contents/Resources/licenses/Opus.txt"
cp "$repo_dir/native/speexdsp/COPYING" "$bundle_dir/Contents/Resources/licenses/SpeexDSP.txt"
cp "$repo_dir/desktop/vendor/gpui-pre-windows/LICENSE-APACHE" "$bundle_dir/Contents/Resources/licenses/GPUI.txt"
cp "$repo_dir/native/client-audio/miniaudio.h" "$icon_tmp/miniaudio.h"
sed -n '/ALTERNATIVE 1 - Public Domain/,$p' "$icon_tmp/miniaudio.h" > "$bundle_dir/Contents/Resources/licenses/miniaudio.txt"
chmod 755 "$bundle_dir/Contents/MacOS/resona-desktop"
if otool -L "$bundle_dir/Contents/MacOS/resona-desktop" | tail -n +2 | grep -E '/opt/homebrew|/usr/local|/Users/'; then
  echo 'Desktop has non-system dependencies; build with the pinned static Opus library' >&2
  exit 1
fi

cat > "$bundle_dir/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleDisplayName</key><string>Resona</string>
  <key>CFBundleExecutable</key><string>resona-desktop</string>
  <key>CFBundleIdentifier</key><string>dev.resona.client</string>
  <key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
  <key>CFBundleName</key><string>Resona</string>
  <key>CFBundleIconFile</key><string>Resona.icns</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.0.0</string>
  <key>CFBundleVersion</key><string>5</string>
  <key>LSMinimumSystemVersion</key><string>12.0</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSMicrophoneUsageDescription</key><string>Resona uses the microphone when you enable voice and unmute it, or explicitly start a local microphone test.</string>
</dict></plist>
PLIST

plutil -replace CFBundleShortVersionString -string "$bundle_version" "$bundle_dir/Contents/Info.plist"
plutil -replace CFBundleVersion -string "$bundle_version" "$bundle_dir/Contents/Info.plist"
plutil -lint "$bundle_dir/Contents/Info.plist"
test -s "$bundle_dir/Contents/Resources/Resona.icns"

python3 - "$bundle_dir" "$bundle_version" <<'PY'
import json
import pathlib
import platform
import subprocess
import sys

bundle = pathlib.Path(sys.argv[1])
info = json.loads(subprocess.check_output([str(bundle / 'Contents/MacOS/resona-desktop'), '--version-json'], text=True))
if info['version'] not in ('v' + sys.argv[2], 'v' + sys.argv[2] + '-dev'):
    raise SystemExit('Desktop and package versions disagree')
arch = {"arm64": "arm64", "x86_64": "amd64"}[platform.machine()]
metadata = dict(schema=1, product="resona", platform="darwin/" + arch,
                version=info['version'], distribution="portable")
(bundle / "Contents/Resources/resona-package.json").write_text(json.dumps(metadata), encoding="utf-8")
PY

if [ -n "$sign_identity" ]; then
  # Sign nested code first; let codesign derive certificate-bound requirements.
  codesign --force --sign "$sign_identity" "$bundle_dir/Contents/Frameworks/libresona_webrtc_apm.dylib"
  codesign --force --sign "$sign_identity" "$bundle_dir"
  codesign --verify --deep --strict --verbose=2 "$bundle_dir"
else
  # Seal the completed bundle even for local/CI builds. Ad-hoc identity still
  # changes with the code hash and does not solve persistent Keychain access.
  codesign --force --sign - "$bundle_dir/Contents/Frameworks/libresona_webrtc_apm.dylib"
  codesign --force --sign - "$bundle_dir"
  codesign --verify --deep --strict --verbose=2 "$bundle_dir"
  echo 'No signing identity: development build; Keychain may prompt again after rebuilds.' >&2
fi

echo "$bundle_dir"
