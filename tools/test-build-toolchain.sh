#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
sandbox=$(mktemp -d "${TMPDIR:-/tmp}/resona-build-check.XXXXXX")
trap 'rm -rf "$sandbox"' EXIT HUP INT TERM
cp "$repo_dir/Makefile" "$sandbox/Makefile"
mkdir -p "$sandbox/build/bin" "$sandbox/desktop/dist" "$sandbox/desktop/target" \
  "$sandbox/build/deploy-check" "$sandbox/build/performance"
touch "$sandbox/build/appicon.png" "$sandbox/build/bin/old-core" \
  "$sandbox/desktop/dist/old-app" "$sandbox/desktop/target/cache" \
  "$sandbox/build/deploy-check/data" "$sandbox/build/performance/report.csv"

# Cleanup must not depend on working Go/Rust installations or remove source/data.
make -C "$sandbox" clean
test ! -e "$sandbox/build/bin"
test ! -e "$sandbox/desktop/dist"
test -f "$sandbox/desktop/target/cache"
test -f "$sandbox/build/appicon.png"
test -f "$sandbox/build/deploy-check/data"
test -f "$sandbox/build/performance/report.csv"
make -C "$sandbox" clean-all
test ! -e "$sandbox/desktop/target"
test -f "$sandbox/build/appicon.png"
test -f "$sandbox/build/deploy-check/data"
test -f "$sandbox/build/performance/report.csv"
make -C "$sandbox" clean

if make -n -C "$sandbox" clean build; then
  echo 'clean and build must not be accepted together' >&2
  exit 1
fi
echo 'Build cleanup checks passed'
