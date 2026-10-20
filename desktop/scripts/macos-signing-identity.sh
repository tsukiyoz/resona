#!/bin/sh
set -eu

if [ -n "${RESONA_CODESIGN_IDENTITY:-}" ]; then
  if [ "$RESONA_CODESIGN_IDENTITY" = - ]; then
    echo 'RESONA_CODESIGN_IDENTITY must be a certificate identity, not ad-hoc (-)' >&2
    exit 1
  fi
  printf '%s\n' "$RESONA_CODESIGN_IDENTITY"
  exit 0
fi

# CI must only use an explicitly provisioned identity, including on PR runners.
if [ -n "${CI:-}" ] || [ -n "${GITHUB_ACTIONS:-}" ]; then
  exit 0
fi

if ! identities=$(security find-identity -v -p codesigning); then
  echo 'Cannot query signing identities; check Keychain access or set RESONA_CODESIGN_IDENTITY.' >&2
  exit 1
fi
identities=$(printf '%s\n' "$identities" | awk '
  $1 ~ /^[0-9]+\)$/ && $2 ~ /^[0-9A-Fa-f]+$/ && length($2) == 40 { print toupper($2) }
' | sort -u)
set -- $identities
case $# in
  0) ;;
  1) printf '%s\n' "$1" ;;
  *)
    echo 'Multiple signing identities found; set RESONA_CODESIGN_IDENTITY to the intended certificate SHA-1.' >&2
    exit 1
    ;;
esac
