#!/bin/sh
set -eu
umask 077

if [ "$#" -eq 1 ]; then
    case "$1" in
        --version|-version) exec resona-server "$@" ;;
    esac
fi

noise_key=/data/noise.key
if [ ! -e "$noise_key" ]; then
    resona-server --init-key --noise-key "$noise_key"
fi
# Existing invalid identities fail in the server; never silently rotate a key.
exec resona-server --noise-key "$noise_key" "$@"
