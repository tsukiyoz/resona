#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
resolver="$repo_dir/desktop/scripts/macos-signing-identity.sh"
unset RESONA_CODESIGN_IDENTITY CI GITHUB_ACTIONS
identities=''
query_status=0
first=1111111111111111111111111111111111111111
second=2222222222222222222222222222222222222222

# Source in a subshell so the resolver uses the mock, never a real Keychain.
security() {
  test "$*" = 'find-identity -v -p codesigning' || return 99
  printf '%s\n' "$identities"
  return "$query_status"
}
resolve() (
  . "$resolver"
)

# Explicit identities take priority even in CI; no enumeration is needed.
query_status=17
RESONA_CODESIGN_IDENTITY='Explicit certificate label'
CI=true
test "$(resolve)" = 'Explicit certificate label'
RESONA_CODESIGN_IDENTITY=-
if resolve; then
  echo 'ad-hoc override was accepted as a certificate' >&2
  exit 1
fi
unset RESONA_CODESIGN_IDENTITY
test -z "$(resolve)"
unset CI
GITHUB_ACTIONS=true
test -z "$(resolve)"
unset GITHUB_ACTIONS
if resolve; then
  echo 'identity enumeration failure was ignored' >&2
  exit 1
fi

query_status=0
identities='     0 valid identities found'
test -z "$(resolve)"
identities="  1) $first \"Apple Development\"
     1 valid identities found"
test "$(resolve)" = "$first"
# Duplicate entries for one fingerprint are still one identity.
identities="$identities
  2) $first \"Same certificate\""
test "$(resolve)" = "$first"
identities="$identities
  3) $second \"Developer ID Application\""
if resolve; then
  echo 'ambiguous signing identity was selected' >&2
  exit 1
fi
echo 'macOS signing identity checks passed'
