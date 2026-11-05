#!/bin/sh
set -eu

# Run inside the isolated Linux container, after all compilation has finished.
cd /work
export GOMAXPROCS=4 RESONA_SERVER_GC_PROBE=1
export RESONA_GC_CHURN=0 RESONA_GC_PERCENT=100 RESONA_GC_PROFILE=
export RESONA_GC_WARMUP_SECONDS=3
export RESONA_GC_SECONDS=${RESONA_GC_SECONDS:-30}
failures=0
for level in ${RESONA_WRITER_LEVELS:-small stress8 stress16}; do
    case "$level" in
        small) members=4; speakers=2; order='baseline-a writer-a batch-a batch-b writer-b baseline-b' ;;
        stress8) members=64; speakers=8; order='writer-a batch-a baseline-a baseline-b batch-b writer-b' ;;
        stress16) members=64; speakers=16; order='batch-a baseline-a writer-a writer-b baseline-b batch-b' ;;
        stress32) members=64; speakers=32; order='baseline-a writer-a batch-a batch-b writer-b baseline-b' ;;
        *) echo "Unknown load level: $level" >&2; exit 1 ;;
    esac
    export RESONA_GC_MEMBERS=$members RESONA_GC_ROOMS=1 RESONA_GC_SPEAKERS_PER_ROOM=$speakers
    for probe_case in $order; do
        model=${probe_case%-*}
        name="$level-$probe_case"
        export RESONA_GC_OUTPUT="/work/$name"
        if [ -e "$RESONA_GC_OUTPUT" ]; then
            echo "Refusing to overwrite $name" >&2
            exit 1
        fi
        cat /sys/fs/cgroup/cpu.stat > "$name.cpu-before"
        echo "Measuring $name"
        if nice -n 10 "./$model.test" -test.run='^TestServerGCProbe$' -test.v -test.timeout=3m > "$name.log" 2>&1; then
            echo "Completed $name"
        else
            failures=$((failures + 1))
            echo "Failed $name; retained $name.log" >&2
        fi
        cat /sys/fs/cgroup/cpu.stat > "$name.cpu-after"
    done
done
test "$failures" -eq 0
