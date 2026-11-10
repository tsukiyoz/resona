#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
desktop_dir=$(dirname -- "$script_dir")
repo_dir=$(dirname -- "$desktop_dir")
bundle="$desktop_dir/dist/Resona.app"
vpk=${RESONA_VPK:-"$repo_dir/build/tools/vpk/vpk"}

[ "$(uname -s)" = Darwin ] || { echo 'macOS is required' >&2; exit 1; }
if [ -d "$repo_dir/build/tools/dotnet" ]; then
  export DOTNET_ROOT=${DOTNET_ROOT:-"$repo_dir/build/tools/dotnet"}
fi
if [ ! -x "$vpk" ]; then
  echo 'Install Velopack CLI 1.2.161 into build/tools/vpk, or set RESONA_VPK. See docs/updates.md.' >&2
  exit 1
fi
sign_identity=$(sh "$script_dir/macos-signing-identity.sh")
[ -n "$sign_identity" ] || { echo 'A certificate signing identity is required for the macOS installer.' >&2; exit 1; }
[ -f "$bundle/Contents/Resources/.resona-bundle" ] || { echo 'Build Resona.app first.' >&2; exit 1; }
codesign --verify --deep --strict "$bundle"

metadata=$(python3 - "$bundle" <<'PY'
import json
import pathlib
import platform
import re
import subprocess
import sys

bundle = pathlib.Path(sys.argv[1])
metadata = json.loads((bundle / 'Contents/Resources/resona-package.json').read_text())
info = json.loads(subprocess.check_output([str(bundle / 'Contents/MacOS/resona-desktop'), '--version-json']))
arch = {'arm64': 'arm64', 'x86_64': 'amd64'}[platform.machine()]
expected = dict(schema=1, product='resona', platform='darwin/' + arch,
                version=info['version'], distribution='portable')
if metadata != expected or not re.fullmatch(r'v\d+\.\d+\.\d+(?:-dev)?', info['version']):
    raise SystemExit('Installer requires a matching portable app and version; run make build-installer-macos.')
print(info['version'], 'arm64' if arch == 'arm64' else 'x64')
PY
)
set -- $metadata
version=$1
arch=$2
[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$bundle/Contents/Info.plist")" = dev.resona.client ]

work=$(mktemp -d "$desktop_dir/dist/.macos-installer.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM
codesign -d --extract-certificates="$work/source-" "$bundle"
source_signer=$(shasum -a 1 "$work/source-0" | cut -d ' ' -f 1)
requested_signer=$(printf '%s' "$sign_identity" | tr '[:upper:]' '[:lower:]')
[ "$source_signer" = "$requested_signer" ] || { echo 'App and installer must use the same certificate fingerprint.' >&2; exit 1; }
codesign -d -r- "$bundle" > "$work/source-requirement" 2>/dev/null

# Velopack modifies a temporary copy; keep the portable bundle and its metadata intact.
"$vpk" pack --packId Resona --packVersion "${version#v}" --packDir "$bundle" \
  --mainExe resona-desktop --packTitle Resona --packAuthors tsukiyoz \
  --runtime "osx-$arch" --outputDir "$work/output" --delta None --noPortable \
  --signAppIdentity "$sign_identity" --signDisableDeep \
  --signEntitlements "$script_dir/macos-installer.entitlements" --skip-updates --yes

set -- "$work/output/"*.pkg
[ "$#" = 1 ] && [ -f "$1" ] || { echo 'Expected exactly one PKG.' >&2; exit 1; }
package=$1
pkgutil --expand-full "$package" "$work/expanded"
set -- "$work/expanded/"*.pkg/Payload/Resona.app
[ "$#" = 1 ] && [ -d "$1" ] || { echo 'Installer does not contain exactly one Resona.app.' >&2; exit 1; }
installed_bundle=$1
codesign --verify --deep --strict --verbose=2 "$installed_bundle"
codesign -d --entitlements - --xml "$installed_bundle" > "$work/entitlements.plist"
[ "$(/usr/libexec/PlistBuddy -c 'Print :com.apple.security.device.audio-input' "$work/entitlements.plist")" = true ]
codesign -d --extract-certificates="$work/installed-" "$installed_bundle"
[ "$(shasum -a 1 "$work/installed-0" | cut -d ' ' -f 1)" = "$source_signer" ]
codesign -d -r- "$installed_bundle" > "$work/installed-requirement" 2>/dev/null
# Paths differ after extraction; compare only the designated requirement.
sed -n '/^designated =>/p' "$work/source-requirement" > "$work/source-dr"
sed -n '/^designated =>/p' "$work/installed-requirement" > "$work/installed-dr"
test -s "$work/source-dr"
cmp "$work/source-dr" "$work/installed-dr"
test -x "$installed_bundle/Contents/MacOS/UpdateMac"
test -s "$installed_bundle/Contents/Resources/sq.version"
cmp "$bundle/Contents/Resources/resona-package.json" "$installed_bundle/Contents/Resources/resona-package.json"
codesign --verify --deep --strict "$bundle"

output="$desktop_dir/dist/macos-installer"
mkdir -p "$output"
destination="$output/Resona-$version-macos-$arch-Setup-unsigned.pkg"
cp "$package" "$destination"
echo 'App certificate signed; PKG unsigned and not notarized.' >&2
echo "$destination"
