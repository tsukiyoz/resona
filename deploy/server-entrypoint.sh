#!/bin/sh
set -eu
umask 077

noise_key=/data/noise.key
if [ ! -e "$noise_key" ]; then
    resona-server --init-key --noise-key "$noise_key"
fi
# Existing invalid identities fail in the server; never silently rotate a key.
exec resona-server --transport noise --noise-key "$noise_key" "$@"
