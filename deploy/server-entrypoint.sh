#!/bin/sh
set -eu
umask 077

if [ "${1:-}" = serve ]; then
    shift
    noise_key=/data/noise.key
    if [ ! -e "$noise_key" ]; then
        resona-server identity init --noise-key "$noise_key"
    fi
    # Existing invalid identities fail in the server; never silently rotate a key.
    exec resona-server serve --noise-key "$noise_key" "$@"
fi

exec resona-server "$@"
