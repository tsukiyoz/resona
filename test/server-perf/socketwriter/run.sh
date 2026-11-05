#!/bin/sh
set -eu

output=${1:-build/server-writer/experiment}
suite=${RESONA_WRITER_SUITE:-quick}
case "$suite" in
    quick)
        probe_cases='64-baseline-a 64-writer-a 64-writer-b 64-baseline-b 4-baseline-a 4-writer-a'
        default_seconds=30
        ;;
    stress)
        probe_cases='stress8-baseline-a stress8-writer-a stress8-writer-b stress8-baseline-b stress16-writer-a stress16-baseline-a stress16-baseline-b stress16-writer-b'
        default_seconds=60
        ;;
    *) echo 'RESONA_WRITER_SUITE must be quick or stress' >&2; exit 1 ;;
esac
mkdir -p "$output"
output=$(cd "$output" && pwd)
if [ -e "$output/baseline.test" ] || [ -e "$output/writer.test" ]; then
    echo "Use a new output directory; existing comparison binaries must be retained: $output" >&2
    exit 1
fi
go run test/server-perf/socketwriter/prepare.go -output "$output/overlay"
go test -c -o "$output/baseline.test" ./internal/server
go test -overlay "$output/overlay/overlay.json" -c -o "$output/writer.test" ./internal/server

export GOMAXPROCS=4 RESONA_SERVER_GC_PROBE=1
export RESONA_GC_CHURN=0 RESONA_GC_PERCENT=100 RESONA_GC_PROFILE=
export RESONA_GC_WARMUP_SECONDS=3
export RESONA_GC_SECONDS=${RESONA_GC_SECONDS:-$default_seconds}
probe_failures=0
for probe_case in $probe_cases; do
    case "$probe_case" in
        64-*) RESONA_GC_MEMBERS=64 RESONA_GC_ROOMS=4 RESONA_GC_SPEAKERS_PER_ROOM=4 ;;
        4-*) RESONA_GC_MEMBERS=4 RESONA_GC_ROOMS=1 RESONA_GC_SPEAKERS_PER_ROOM=2 ;;
        stress8-*) RESONA_GC_MEMBERS=64 RESONA_GC_ROOMS=1 RESONA_GC_SPEAKERS_PER_ROOM=8 ;;
        stress16-*) RESONA_GC_MEMBERS=64 RESONA_GC_ROOMS=1 RESONA_GC_SPEAKERS_PER_ROOM=16 ;;
    esac
    case "$probe_case" in *-baseline-*) probe_binary=baseline ;; *) probe_binary=writer ;; esac
    export RESONA_GC_MEMBERS RESONA_GC_ROOMS RESONA_GC_SPEAKERS_PER_ROOM
    RESONA_GC_OUTPUT="$output/$probe_case"
    export RESONA_GC_OUTPUT
    if [ -e "$RESONA_GC_OUTPUT" ]; then
        echo "Refusing to replace an existing run: $RESONA_GC_OUTPUT" >&2
        exit 1
    fi
    echo "Measuring: $probe_case"
    if "$output/$probe_binary.test" -test.run='^TestServerGCProbe$' -test.v -test.timeout=3m > "$output/$probe_case.log" 2>&1; then
        echo "Completed: $probe_case"
    else
        probe_status=$?
        probe_failures=$((probe_failures + 1))
        echo "Failed: $probe_case (exit $probe_status), see $output/$probe_case.log" >&2
    fi
done
if [ "$probe_failures" -ne 0 ]; then
    echo "Failed runs: $probe_failures (retained; no automatic reruns)" >&2
    exit 1
fi
