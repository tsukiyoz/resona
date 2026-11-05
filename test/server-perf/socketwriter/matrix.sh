#!/bin/sh
set -eu

output=${1:-build/server-writer/queue-matrix}
mkdir -p "$output"
output=$(cd "$output" && pwd)
for model in baseline writer; do
    for slots in 4 16; do
        if [ -e "$output/$model-q$slots.test" ]; then
            echo "Use a new output directory; retaining existing binaries: $output" >&2
            exit 1
        fi
    done
done
for model in baseline writer; do
    for slots in 4 16; do
        overlay="$output/overlay-$model-q$slots"
        go run test/server-perf/socketwriter/prepare.go -output "$overlay" -model "$model" -slots "$slots"
        go test -overlay "$overlay/overlay.json" -c -o "$output/$model-q$slots.test" ./internal/server
    done
done

export GOMAXPROCS=4 RESONA_SERVER_GC_PROBE=1
export RESONA_GC_CHURN=0 RESONA_GC_PERCENT=100 RESONA_GC_PROFILE=
export RESONA_GC_WARMUP_SECONDS=3
export RESONA_GC_SECONDS=${RESONA_GC_SECONDS:-60}
export RESONA_GC_MEMBERS=64 RESONA_GC_ROOMS=1
probe_failures=0
for speakers in 8 16; do
    if [ "$speakers" -eq 8 ]; then
        cases='baseline-q4-a writer-q4-a writer-q16-a baseline-q16-a baseline-q16-b writer-q16-b writer-q4-b baseline-q4-b'
    else
        cases='writer-q16-a baseline-q16-a baseline-q4-a writer-q4-a writer-q4-b baseline-q4-b baseline-q16-b writer-q16-b'
    fi
    RESONA_GC_SPEAKERS_PER_ROOM=$speakers
    export RESONA_GC_SPEAKERS_PER_ROOM
    for probe_case in $cases; do
        binary=${probe_case%-*}
        name="stress$speakers-$probe_case"
        RESONA_GC_OUTPUT="$output/$name"
        export RESONA_GC_OUTPUT
        if [ -e "$RESONA_GC_OUTPUT" ]; then
            echo "Refusing to replace an existing run: $RESONA_GC_OUTPUT" >&2
            exit 1
        fi
        echo "Measuring: $name"
        if "$output/$binary.test" -test.run='^TestServerGCProbe$' -test.v -test.timeout=3m > "$output/$name.log" 2>&1; then
            echo "Completed: $name"
        else
            probe_status=$?
            probe_failures=$((probe_failures + 1))
            echo "Failed: $name (exit $probe_status), see $output/$name.log" >&2
        fi
    done
done
if [ "$probe_failures" -ne 0 ]; then
    echo "Failed runs: $probe_failures (retained; no automatic reruns)" >&2
    exit 1
fi
