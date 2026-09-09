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
if [ -d "$bundle_dir" ] && [ ! -f "$bundle_dir/Contents/.resona-bundle" ]; then
  echo "refusing to replace an unrecognized app bundle: $bundle_dir" >&2
  exit 1
fi
rm -rf "$bundle_dir"
mkdir -p "$bundle_dir/Contents/MacOS" "$bundle_dir/Contents/Resources"
touch "$bundle_dir/Contents/.resona-bundle"
cp "$desktop_dir/target/$profile/resona-desktop" "$bundle_dir/Contents/MacOS/resona-desktop"
cp "$core_binary" "$bundle_dir/Contents/MacOS/resona-core"
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
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.0.1</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>LSMinimumSystemVersion</key><string>12.0</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSMicrophoneUsageDescription</key><string>Resona uses the microphone when you enable voice and unmute it, or explicitly start a local microphone test.</string>
</dict></plist>
PLIST

echo "$bundle_dir"
