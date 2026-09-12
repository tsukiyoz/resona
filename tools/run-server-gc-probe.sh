#!/bin/sh
set -eu

# Run from the repository root. Each mode gets a fresh isolated server process.
probe_output=${1:-build/bin/gc-probe}
mkdir -p "$probe_output"
probe_output=$(cd "$probe_output" && pwd)
go test -c -o "$probe_output/server-probe.test" ./internal/server
for probe_round in 1 2 3; do
    case "$probe_round" in
        2) probe_modes='off 100' ;;
        *) probe_modes='100 off' ;;
    esac
    for probe_gc in $probe_modes; do
        RESONA_SERVER_GC_PROBE=1 RESONA_GC_SECONDS=10 \
        RESONA_GC_MEMBERS=64 RESONA_GC_ROOMS=4 \
        RESONA_GC_CHURN=0 RESONA_GC_DIAGNOSTIC=0 \
        RESONA_GC_PERCENT="$probe_gc" \
        RESONA_GC_OUTPUT="$probe_output/ab-$probe_round-$probe_gc" \
        "$probe_output/server-probe.test" -test.run='^TestServerGCProbe$' \
        -test.v -test.timeout=1m
    done
done
