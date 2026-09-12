#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
desktop_dir=$(dirname -- "$script_dir")
repo_dir=$(dirname -- "$desktop_dir")
core_binary=${RESONA_CORE_BINARY:-"$repo_dir/build/bin/resona-core"}
bundle_dir=${RESONA_BUNDLE_DIR:-"$desktop_dir/dist/Resona.app"}
profile=${RESONA_CARGO_PROFILE:-release}

case "$profile" in
  debug|release) ;;
  *) echo "RESONA_CARGO_PROFILE must be debug or release" >&2; exit 1 ;;
esac

case "$bundle_dir" in
  "$desktop_dir/dist/"*.app) ;;
  *) echo "bundle output must be an .app below $desktop_dir/dist" >&2; exit 1 ;;
esac

if [ ! -x "$core_binary" ]; then
  echo "resona-core is missing or not executable: $core_binary" >&2
  exit 1
fi

if [ "$profile" = release ]; then
  cargo build --manifest-path "$desktop_dir/Cargo.toml" --release
else
  cargo build --manifest-path "$desktop_dir/Cargo.toml"
fi

icon_tmp=$(mktemp -d "${TMPDIR:-/tmp}/resona-icon.XXXXXX")
trap 'rm -rf "$icon_tmp"' EXIT HUP INT TERM
mkdir "$icon_tmp/Resona.iconset"
for size in 16 32 128 256 512; do
  sips -z "$size" "$size" "$repo_dir/build/appicon.png" \
    --out "$icon_tmp/Resona.iconset/icon_${size}x${size}.png" >/dev/null
  double_size=$((size * 2))
  sips -z "$double_size" "$double_size" "$repo_dir/build/appicon.png" \
    --out "$icon_tmp/Resona.iconset/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil -c icns "$icon_tmp/Resona.iconset" -o "$icon_tmp/Resona.icns"

if [ -d "$bundle_dir" ] && [ ! -f "$bundle_dir/Contents/.resona-bundle" ]; then
  echo "refusing to replace an unrecognized app bundle: $bundle_dir" >&2
  exit 1
fi
rm -rf "$bundle_dir"
mkdir -p "$bundle_dir/Contents/MacOS" "$bundle_dir/Contents/Resources"
touch "$bundle_dir/Contents/.resona-bundle"
cp "$desktop_dir/target/$profile/resona-desktop" "$bundle_dir/Contents/MacOS/resona-desktop"
cp "$core_binary" "$bundle_dir/Contents/MacOS/resona-core"
cp "$icon_tmp/Resona.icns" "$bundle_dir/Contents/Resources/Resona.icns"
cp "$repo_dir/docs/noise-license.txt" "$bundle_dir/Contents/Resources/noise-license.txt"
chmod 755 "$bundle_dir/Contents/MacOS/resona-desktop" "$bundle_dir/Contents/MacOS/resona-core"

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
  <key>CFBundleShortVersionString</key><string>0.0.2</string>
  <key>CFBundleVersion</key><string>2</string>
  <key>LSMinimumSystemVersion</key><string>12.0</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSMicrophoneUsageDescription</key><string>Resona uses the microphone when you enable voice and unmute it, or explicitly start a local microphone test.</string>
</dict></plist>
PLIST

plutil -lint "$bundle_dir/Contents/Info.plist"
test -s "$bundle_dir/Contents/Resources/Resona.icns"

echo "$bundle_dir"
